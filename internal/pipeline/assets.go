package pipeline

import (
	"embed"
	"io/fs"
)

// promptsAndWorkflows ships the pipeline's prompts + ComfyUI workflow
// alongside the binary. Adding a new prompt is "drop the .md in
// prompts/" — embed.FS picks it up at compile time.
//
//go:embed prompts/*.md workflows/*.json
var promptsAndWorkflows embed.FS

// promptsFS narrows the embed to the prompts/ subtree so PromptFS calls
// can reference files by their bare name (e.g. "describe_image.md")
// instead of "prompts/describe_image.md".
var promptsFS fs.FS = mustSub(promptsAndWorkflows, "prompts")

// workflowsFS narrows the embed to the workflows/ subtree so
// WorkflowFS calls reference workflows by bare name.
var workflowsFS fs.FS = mustSub(promptsAndWorkflows, "workflows")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
