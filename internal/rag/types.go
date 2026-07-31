// Package rag turns a etude pipeline run's
// processed_lessons.json into a portable retrieval-augmented-generation
// dataset: chunked + embedded content, plus study aids (flashcards,
// glossary, key-numbers reference, equation cheat sheet).
//
// Architectural note: this package runs as a CLI post-processor
// alongside the vamp pipeline, not as a pipeline stage. The chunking
// and enrichment logic is naturally sequential Go code rather than
// template gymnastics, so it lives here.
package rag

// ProcessedLesson mirrors one item in processed_lessons.json — the
// output of the textbook pipeline's merge_lessons render. Only the
// fields the RAG flow uses are typed; the LLM may produce additional
// keys (e.g. related_images) that we ignore.
type ProcessedLesson struct {
	Lesson         string            `json:"lesson"`
	LectureContent string            `json:"lecture_content"`
	Definitions    map[string]string `json:"definitions"`
	KeyNumbers     map[string]string `json:"key_numbers"`
	Processes      []string          `json:"processes"`
	CommonMistakes []string          `json:"common_mistakes"`
	ExamFocus      []string          `json:"exam_focus"`
}

// ProcessedLessons is the top-level shape of processed_lessons.json.
type ProcessedLessons struct {
	Items []ProcessedLesson `json:"items"`
}

// ChunkType discriminates a chunk row by what it represents.
type ChunkType string

const (
	// ChunkTypeProse is a ~485-token (1600-char) window of lecture_content.
	ChunkTypeProse ChunkType = "prose"
	// ChunkTypeDefinition is a single term + definition.
	ChunkTypeDefinition ChunkType = "definition"
	// ChunkTypeKeyNumber is a single numeric value + its context.
	ChunkTypeKeyNumber ChunkType = "key_number"
	// ChunkTypeProcess is a single process description.
	ChunkTypeProcess ChunkType = "process"
)

// Chunk is one row in chunks.jsonl. Carries the source text plus the
// hierarchical metadata that lets a retrieval result trace back to a
// specific lesson and chunk type.
type Chunk struct {
	// ID is "<module>_l<NNN>_<type>_<idx>" (NNN = zero-padded lesson
	// index) — stable across regenerations of the same input +
	// chunker settings.
	ID string `json:"id"`
	// Module is the run's module name (e.g. "module_1").
	Module string `json:"module"`
	// LessonIndex is the 0-indexed position of this lesson in the
	// processed_lessons.json items array.
	LessonIndex int `json:"lesson_index"`
	// LessonTitle is the .lesson field of the source ProcessedLesson.
	LessonTitle string `json:"lesson_title"`
	// Type discriminates how to interpret Text + Extra.
	Type ChunkType `json:"type"`
	// Idx is the position within this lesson's chunks of the same
	// Type. Lets the chunker emit multiple prose windows per lesson
	// without colliding IDs.
	Idx int `json:"idx"`
	// Text is the embeddable surface — what the embedder POSTs to
	// /v1/embeddings. Definition chunks pack "term: definition" so
	// queries about either side hit the chunk.
	Text string `json:"text"`
	// Term, when set, is the dictionary key (definition or key
	// number). Definition chunks set Term to the term; key-number
	// chunks set Term to the numeric string.
	Term string `json:"term,omitempty"`
	// Embedding is the 1024-dim vector from bge-large. Populated by
	// the embed step; nil before that.
	Embedding []float32 `json:"embedding,omitempty"`
	// Enrichment carries the LLM-generated multiple-choice + short-
	// answer questions for this chunk. Populated by the enrich step
	// for prose chunks only (definitions / key numbers / processes
	// are short enough that the chunk itself is the answer).
	Enrichment *Enrichment `json:"enrichment,omitempty"`
}

// Enrichment is the LLM-generated study material for one prose chunk.
type Enrichment struct {
	Summary        string           `json:"summary"`
	KeyTerms       []string         `json:"key_terms"`
	MultipleChoice []MultipleChoice `json:"multiple_choice"`
	ShortAnswer    []ShortAnswer    `json:"short_answer"`
}

// MultipleChoice is one MC question. CorrectIndex points at the right
// option in the Options slice (0-indexed).
type MultipleChoice struct {
	Question     string   `json:"question"`
	Options      []string `json:"options"`
	CorrectIndex int      `json:"correct_index"`
	Explanation  string   `json:"explanation,omitempty"`
}

// ShortAnswer is one open-ended question + its model answer (1-2
// sentences).
type ShortAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Manifest is manifest.json at the root of the RAG export dir. Lets a
// future tool know whether the embedding model + chunk parameters in
// the export are compatible with whatever pipeline is reading it.
type Manifest struct {
	GeneratedAt    string `json:"generated_at"` // RFC3339
	Module         string `json:"module"`
	EmbeddingModel string `json:"embedding_model"` // e.g. "bge-large-en-v1.5-q8"
	EmbeddingDims  int    `json:"embedding_dims"`  // 1024 for bge-large
	ChunkMaxChars  int    `json:"chunk_max_chars"`
	NumLessons     int    `json:"num_lessons"`
	NumChunks      int    `json:"num_chunks"`
	NumDefinitions int    `json:"num_definitions"`
	NumKeyNumbers  int    `json:"num_key_numbers"`
	NumProcesses   int    `json:"num_processes"`
	NumEquations   int    `json:"num_equations"`
	SourceFile     string `json:"source_file"` // path to processed_lessons.json
}
