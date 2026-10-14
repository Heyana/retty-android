package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

var fileEditorFont = ui.Font{Family: "JetBrains Mono, monospace", Size: 13}

// Keep the official editor state with the loaded preview, including when the
// preview moves into its expanded dialog. Browsing never changes the file.
func fileCodePreview(path, text string) *editor.State {
	buffer := editor.NewBuffer(text)
	buffer.SetLanguage(editor.LanguageFor(path))
	return &editor.State{
		Buffer: buffer, ReadOnly: true, Font: fileEditorFont,
		NoActiveLine: true, NoScrollPastEnd: true, Label: "文件预览",
	}
}

func filePreviewLanguage(path string) string {
	if language := editor.LanguageFor(path); language != nil {
		return language.Name
	}
	return ""
}

// The server returns one file's patch, including its original line numbers.
// SetPatch displays its hunks directly, without another read of the whole file.
func fileDiffPreview(path, text string) (*editor.DiffState, error) {
	patches, err := editor.ParsePatch(text)
	if err != nil {
		return nil, err
	}
	if len(patches) == 0 || len(patches[0].Hunks) == 0 {
		return nil, nil
	}
	state := &editor.DiffState{Layout: editor.DiffUnified, Font: fileEditorFont}
	state.SetPatch(patches[0])
	language := editor.LanguageFor(path)
	state.Old.SetLanguage(language)
	state.New.SetLanguage(language)
	return state, nil
}
