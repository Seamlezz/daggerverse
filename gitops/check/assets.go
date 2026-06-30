package check

import (
	"fmt"
	"io/fs"
	"path"
)

type Scripts struct {
	files map[string]string
}

func loadScripts(fsys fs.FS, dir string) (Scripts, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return Scripts{}, err
	}

	files := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		content, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return Scripts{}, err
		}
		files[name] = string(content)
	}
	return Scripts{files: files}, nil
}

func (s Scripts) get(name string) string {
	content, ok := s.files[name]
	if ok {
		return content
	}
	panic(fmt.Sprintf("missing check script %q", name))
}
