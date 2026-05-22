package pipeline

import (
	"strconv"
	"time"

	"github.com/gallowaysoftware/vibe/vamp"
)

// Build constructs the textbook-to-audiobook pipeline from a Config.
// The DAG mirrors the CIBD pipeline's module.yaml the binary was lifted
// from, with Config-tunable knobs (subject voice, cover art prompt,
// EPUB metadata, output filename templates) pushed into pipeline inputs
// so the prompts reference them via `{{ .inputs.<name> }}` instead of
// having the curriculum-specific surface baked in.
//
// The caller (typically the binary's cobra factory closure) builds a
// Config from CLI flags, applies WithDefaults, and hands the result to
// Build. The returned pipeline is already validated.
func Build(cfg Config) (*vamp.Pipeline, error) {
	cfg = cfg.WithDefaults()
	// Required-input validation happens at vamp run time via the
	// Input("...", vamp.Required()) declarations below — duplicating
	// that here would prevent subcommands like `requirements` /
	// `validate` from working with a fresh Config (no flags
	// supplied, no module to build yet).

	p := vamp.New("textbook-to-audiobook").
		Describe("Turn a directory of structured lessons (markdown + diagrams) into a chapterised audiobook (M4B), per-unit MP3s, a markdown study guide, and an EPUB companion. Generic re-packaging of the cibd-distilling pipeline.")

	// ---- Inputs ---------------------------------------------------------
	// Required at run time.
	p.Input("module_num", vamp.Required(),
		vamp.WithDefault(strconv.Itoa(cfg.ModuleNum)),
		vamp.Describe("Numeric module identifier; used in cover seeds + output filenames."))
	p.Input("module_topic", vamp.Required(),
		vamp.WithDefault(cfg.ModuleTopic),
		vamp.Describe("Short prose summary (e.g. 'fermentation and wort production')."))
	p.Input("lesson_root", vamp.Required(),
		vamp.WithDefault(cfg.LessonRoot),
		vamp.Describe("Directory containing Lesson_*/ subdirectories with lesson.md + images/."))

	// Optional with sensible defaults.
	p.Input("search_suffix", vamp.WithDefault(cfg.SearchSuffix),
		vamp.Describe("Appended to every web-search query for topic enrichment."))
	p.Input("voice", vamp.WithDefault(cfg.Voice),
		vamp.Describe("Kokoro voice (e.g. af_bella, am_adam)."))
	p.Input("subject_label", vamp.WithDefault(cfg.SubjectLabel),
		vamp.Describe("Field-of-study label woven into every prompt."))
	p.Input("program_label", vamp.WithDefault(cfg.ProgramLabel),
		vamp.Describe("Program / course this material belongs to."))
	p.Input("expert_persona", vamp.WithDefault(cfg.ExpertPersona),
		vamp.Describe("First-person identity the lecturer prompts adopt."))
	p.Input("assessment_label", vamp.WithDefault(cfg.AssessmentLabel),
		vamp.Describe("How learners are evaluated (drives the 'exam pointer' callouts)."))
	p.Input("cover_prompt", vamp.WithDefault(cfg.CoverPrompt),
		vamp.Describe("SDXL prompt for the module cover."))
	p.Input("epub_title", vamp.WithDefault(cfg.EPUBTitleTemplate),
		vamp.Describe("EPUB title (templated against module_num + module_topic)."))
	p.Input("epub_author", vamp.WithDefault(cfg.EPUBAuthor),
		vamp.Describe("EPUB author metadata."))

	// ---- Requirements ---------------------------------------------------
	p.RequireService("searxng", "http://127.0.0.1:14002",
		"SearXNG meta-search for topic enrichment.",
		"docker compose -f ~/.config/vibe/compose/searxng/docker-compose.yaml up -d")
	p.RequireService("kokoro-fastapi", "http://127.0.0.1:8880",
		"OpenAI-compatible TTS server (Kokoro). Activated as the 'tts' vibe profile.",
		"vibe profile activate tts_kokoro")
	p.RequireGPUMemory("32GB")
	p.RequireDiskSpace("20GB per module run (intermediate artefacts + 500MB final deliverables)")
	p.Note("Tested against Qwen3.6-27B-MTP @ 131k context for the long_form capability and Gemma 3 27B + mmproj for vision.")
	p.Note("A complete module run is ~5 hours on a single RTX 5090.")
	p.CapabilityModel("long_form", vamp.ModelHint{
		MinParams: "27B", MinContext: 131072,
		SuggestedModel: "qwen3.6-27b-mtp-q6_k",
		Capabilities:   []string{"text"},
	})
	p.CapabilityModel("vision", vamp.ModelHint{
		MinParams: "27B", MinContext: 32768,
		SuggestedModel: "gemma-3-27b + mmproj",
		Capabilities:   []string{"text", "vision"},
	})
	p.CapabilityModel("tts", vamp.ModelHint{
		SuggestedModel: "kokoro-fastapi (http_server backend)",
	})
	p.CapabilityModel("image_gen", vamp.ModelHint{
		SuggestedModel: "SDXL-Turbo via ComfyUI",
	})

	// ---- Stages ---------------------------------------------------------

	// Enumerate lesson directories for foreach processing.
	listLessons := p.Render("list_lessons").
		Prompt(`{{ enumerateLessons (joinPath .inputs.lesson_root "Lesson_*") }}`).
		Output("lesson_list.json").
		OutputFormatJSON()

	// Unique-image fan-out: dedup every image referenced by any lesson
	// by content sha256. Identical diagrams across lessons share one
	// vision pass.
	flattenUnique := p.Render("flatten_unique_images").
		After(listLessons).
		Prompt(`{{ enumerateUniqueImages .inputs.lesson_root .stages.list_lessons.output }}`).
		Output("unique_images.json").
		OutputFormatJSON()

	// One vision call per UNIQUE image content. SVGs are rasterised to
	// fit a single Gemma 3 tile (896x896) with the SVG text labels
	// extracted as a sidecar so the model has ground-truth strings
	// even for small fonts.
	describeImage := p.Text("describe_image").
		Capability("vision").
		After(flattenUnique).
		Foreach(flattenUnique, "img").
		PromptFS(promptsFS, "describe_image.md").
		ImageFile("{{ .img.path }}").
		OutputFormatJSON().
		Output("image_desc/{{.img.hash}}.json").
		Param("temperature", 0.3).
		Param("max_tokens", 1000).
		Retry(&vamp.RetryPolicy{
			MaxAttempts:    5,
			InitialBackoff: 15 * time.Second,
			MaxBackoff:     120 * time.Second,
			RetryOn:        []string{"transient", "invalid_output"},
		})

	// Per-lesson brevity pass: SAQ lessons reference 20-40+ diagrams,
	// the raw concatenation blows long_form's context. Compact targets
	// 50K chars / ~12K tokens per lesson.
	compactDiagrams := p.Compact("compact_lesson_diagrams").
		Capability("long_form").
		After(listLessons, describeImage).
		Foreach(listLessons, "lesson").
		Source(`{{ imageDescriptionsForLesson .runDir .inputs.lesson_root .lesson }}`).
		TargetChars(50000).
		ChunkChars(30000).
		Preserve("numerical labels (temperatures, pressures, ratios, dimensions, axis values), equipment names, flow directions, and the concept each diagram illustrates").
		Output("diagrams_compact/{{.lesson}}.md")

	// Text-only lecture-notes pass: folds compacted diagram descriptions
	// INTO each lesson's lecture_content as prose.
	processLesson := p.Text("process_lesson").
		Capability("long_form").
		After(listLessons, compactDiagrams).
		Foreach(listLessons, "lesson").
		PromptFS(promptsFS, "process_single_lesson.md").
		OutputFormatJSON().
		Output("processed/{{.lesson}}.json").
		Param("temperature", 0.3).
		Param("max_tokens", 65536).
		Retry(&vamp.RetryPolicy{
			MaxAttempts:    5,
			InitialBackoff: 30 * time.Second,
			MaxBackoff:     180 * time.Second,
			RetryOn:        []string{"transient", "invalid_output"},
		})

	// String-join per-lesson outputs into one items array.
	mergeLessons := p.Render("merge_lessons").
		After(processLesson).
		Prompt(`{"items": {{ mergeJSON .stages.process_lesson.output }} }`).
		Output("processed_lessons.json").
		OutputFormatJSON()

	// LLM-summarised, context-fitting version of the full lesson corpus.
	compactLessons := p.Compact("compact_lessons").
		Capability("long_form").
		After(mergeLessons).
		Source(`{{ .stages.merge_lessons.output }}`).
		TargetChars(100000).
		Preserve("the lesson title, every numerical value, every named entity (enzyme/process/equipment), every cause-and-effect relationship, and every SAQ question + model answer").
		Output("compact_lessons.txt")

	// Identify thematic units in this module + judge per-unit duration.
	extractUnits := p.Text("extract_units").
		Capability("long_form").
		After(compactLessons).
		PromptFS(promptsFS, "extract_units.md").
		OutputFormatJSON().
		Output("units.json").
		Param("temperature", 0.2).
		Param("max_tokens", 16384).
		Retry(&vamp.RetryPolicy{
			MaxAttempts:    3,
			InitialBackoff: 5 * time.Second,
			MaxBackoff:     30 * time.Second,
			RetryOn:        []string{"transient", "invalid_output"},
		})

	// Topics for web-search enrichment (reads compact_lessons, not the
	// raw merge — the raw can blow 131K context on large modules).
	extractTopics := p.Text("extract_topics").
		Capability("long_form").
		After(compactLessons).
		PromptFS(promptsFS, "extract_topics.md").
		OutputFormatJSON().
		Output("topics.json").
		Param("temperature", 0.2).
		Param("max_tokens", 8192)

	// Web search per topic. URL-encoded so '&'/'+'/'%' in topics don't
	// corrupt the request. Cached (idempotent reads).
	search := p.Webhook("search").
		After(extractTopics).
		Foreach(extractTopics, "topic").
		URL(`http://127.0.0.1:14002/search?format=json&q={{ .topic | urlencode }}+{{ .inputs.search_suffix | urlencode }}`).
		Method("GET").
		Cache(true).
		Assert(&vamp.AssertSpec{StatusCode: 200}).
		Output("search_{{.i}}.json")
	// Render stages don't have OutputFormat in some cases; webhook
	// stages don't expose it via the DSL — the search output is JSON
	// by virtue of the body shape, which is enough for downstream
	// foreaches.

	// Compact ~400KB of SearXNG noise down to ~40KB of useful context.
	compactSearch := p.Compact("compact_search").
		Capability("long_form").
		After(search).
		Source(`{{ .stages.search.output }}`).
		TargetChars(40000).
		Preserve("every organisation name, brand, product name, numerical specification, year, geographic origin, regulatory body, and concrete worked example. Drop search engine metadata, URLs (unless integral to a fact), and duplicate result snippets.").
		Output("compact_search.txt")

	// Derive the distinct PARENT unit list. uniqueByKey preserves
	// first-occurrence order so MP3s land in natural unit order.
	uniqueParents := p.Render("unique_parents").
		After(extractUnits).
		Prompt(`{{ uniqueByKey "parent_unit_id" .stages.extract_units.output }}`).
		Output("unique_parents.json").
		OutputFormatJSON()

	// Per-pass script generation. Foreach over the (potentially
	// exploded) sub-units. Each call asks for <=60 min of content which
	// lands within Qwen's reliable per-call output ceiling.
	genScript := p.Text("generate_unit_script").
		Capability("long_form").
		After(extractUnits, compactLessons, compactSearch).
		Foreach(extractUnits, "unit").
		PromptFS(promptsFS, "generate_unit_script.md").
		OutputFormatJSON().
		Output("scripts/{{.unit.unit_id}}.json").
		Param("temperature", 0.7).
		Param("max_tokens", 49152).
		Retry(&vamp.RetryPolicy{
			MaxAttempts:    3,
			InitialBackoff: 10 * time.Second,
			MaxBackoff:     60 * time.Second,
			RetryOn:        []string{"transient", "invalid_output"},
		})

	// Module-wide written companion (markdown).
	studyGuide := p.Text("study_guide").
		Capability("long_form").
		After(compactLessons, compactSearch).
		PromptFS(promptsFS, "study_guide.md").
		Output(cfg.StudyGuideFilenameTemplate).
		Param("temperature", 0.4).
		Param("max_tokens", 32768)

	// Flatten per-unit scripts into a flat segment list, stamping each
	// segment with its owning parent unit_id.
	flattenSegments := p.Render("flatten_segments").
		After(genScript).
		Prompt(`{{ flattenItems .stages.generate_unit_script.output }}`).
		Output("all_segments.json").
		OutputFormatJSON()

	// Split each segment into TTS-sized chunks at natural pause
	// boundaries (sentence ends, never mid-formula).
	chunkForTTS := p.Text("chunk_for_tts").
		Capability("long_form").
		After(flattenSegments).
		Foreach(flattenSegments, "segment").
		PromptFS(promptsFS, "chunk_for_tts.md").
		OutputFormatJSON().
		Output("chunks/{{.segment.unit_id}}_seg{{.i}}.json").
		Param("temperature", 0.1).
		Param("max_tokens", 16384).
		Retry(&vamp.RetryPolicy{
			MaxAttempts:    3,
			InitialBackoff: 5 * time.Second,
			MaxBackoff:     30 * time.Second,
			RetryOn:        []string{"transient", "invalid_output"},
		})

	// Flatten per-segment chunk lists into one flat chunk list.
	flattenChunks := p.Render("flatten_chunks").
		After(chunkForTTS).
		Prompt(`{{ flattenItems .stages.chunk_for_tts.output }}`).
		Output("all_chunks.json").
		OutputFormatJSON()

	// TTS via Kokoro. The audio stage's default retry policy (added in
	// vamp v0.5+) covers transient warm-up failures.
	audio := p.Audio("audio").
		Capability("tts").
		After(flattenChunks).
		Foreach(flattenChunks, "chunk").
		Engine(vamp.AudioEngineKokoro).
		Voice("{{.inputs.voice}}").
		TextTemplate("{{.chunk.text}}").
		Output("audio/{{.chunk.unit_id}}/chunk_{{.i}}.wav")

	// Per-PARENT-unit MP3 concat. Foreach over the dedup'd parents.
	mp3 := p.FFmpeg("mp3").
		After(audio, uniqueParents).
		Foreach(uniqueParents, "parent").
		ConcatWavs().
		ConcatWavsDir("audio/{{ .parent.parent_unit_id }}").
		Output(cfg.UnitMP3FilenameTemplate)

	// Cover art via ComfyUI / SDXL-Turbo. Static prompt baked from
	// inputs.cover_prompt so the generated image is deterministic per
	// module_num.
	coverArt := p.ComfyUI("cover_art").
		Capability("image_gen").
		After(extractUnits).
		WorkflowFS(workflowsFS, "sdxl_turbo.json").
		Parameter("4.text", `{{ .inputs.cover_prompt }}`).
		Parameter("7.seed", `{{ .inputs.module_num }}42`).
		Output("cover.png")

	// Audiobook bundle: chapterised M4B. m4b_from drives chapter order
	// from unique_parents (natural unit order). cover_image embeds the
	// SDXL render.
	p.FFmpeg("m4b").
		After(mp3, uniqueParents, coverArt).
		M4BFrom(uniqueParents).
		M4BVar("parent").
		M4BFile(cfg.UnitMP3FilenameTemplate).
		M4BChapter("{{ .parent.parent_unit_title }}").
		CoverImage("cover.png").
		Output(cfg.AudiobookFilenameTemplate)

	// EPUB study guide via pandoc/docker. cover_image embeds the SDXL
	// render as --epub-cover-image.
	p.Pandoc("epub").
		After(studyGuide, coverArt).
		SourceFile(cfg.StudyGuideFilenameTemplate).
		From("markdown").
		To("epub").
		Metadata("title", cfg.EPUBTitleTemplate).
		Metadata("author", cfg.EPUBAuthor).
		Metadata("lang", "en-US").
		Args("--toc", "--toc-depth=3").
		CoverImage("cover.png").
		Output(cfg.EPUBFilenameTemplate)

	return p.Build()
}
