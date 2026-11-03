package nfqws

import (
	"os"
	"slices"
	"strings"
)

type ListFile struct {
	Name     string `json:"name"`
	Group    string `json:"group"`
	ReadOnly bool   `json:"read_only"`
}

var standardLists = []string{"user.list", "exclude.list", "auto.list", "ipset.list", "ipset_exclude.list"}

// ListFiles enumerates regular NFQWS list files without following symlinks.
func ListFiles(dir string) ([]ListFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	standard := make([]ListFile, len(standardLists))
	additional := make([]ListFile, 0)
	archives := make([]ListFile, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || strings.HasPrefix(name, ".") {
			continue
		}
		if index := slices.Index(standardLists, name); index >= 0 {
			standard[index] = ListFile{Name: name, Group: "standard"}
			continue
		}
		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, ".list"):
			additional = append(additional, ListFile{Name: name, Group: "additional"})
		case strings.HasSuffix(lower, ".list.gz"), strings.HasSuffix(lower, ".list-opkg"), strings.HasSuffix(lower, ".list-old"):
			archives = append(archives, ListFile{Name: name, Group: "archive", ReadOnly: strings.HasSuffix(lower, ".gz")})
		}
	}
	files := make([]ListFile, 0, len(standard)+len(additional)+len(archives))
	for _, file := range standard {
		if file.Name != "" {
			files = append(files, file)
		}
	}
	return append(append(files, additional...), archives...), nil
}
