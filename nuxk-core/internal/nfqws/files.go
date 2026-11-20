package nfqws

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func Files(dir, section string) ([]ListFile, error) {
	switch section {
	case "lists":
		return ListFiles(filepath.Join(dir, section))
	case "config", "scripts":
		subdir := dir
		if section == "scripts" {
			subdir = filepath.Join(dir, "lua")
		}
		entries, err := os.ReadDir(subdir)
		if err != nil {
			return nil, err
		}
		files := []ListFile{}
		for _, entry := range entries {
			if entry.Type().IsRegular() && allowedFile(section, entry.Name()) {
				files = append(files, ListFile{Name: entry.Name(), ReadOnly: strings.HasSuffix(entry.Name(), ".gz")})
			}
		}
		return files, nil
	default:
		return nil, os.ErrInvalid
	}
}

func allowedFile(section, name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\:\x00") {
		return false
	}
	switch section {
	case "config":
		return name == "nfqws2.conf" || name == "nfqws2.conf-opkg" || name == "nfqws2.conf-old"
	case "lists":
		lower := strings.ToLower(name)
		return strings.HasSuffix(lower, ".list") || strings.HasSuffix(lower, ".list.gz") || strings.HasSuffix(lower, ".list-opkg") || strings.HasSuffix(lower, ".list-old")
	case "scripts":
		return strings.HasSuffix(name, ".lua") || strings.HasSuffix(name, ".lua.gz")
	}
	return false
}

func ReadFile(dir, section, name string) (string, error) {
	if !allowedFile(section, name) {
		return "", os.ErrInvalid
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	path := name
	switch section {
	case "lists":
		path = filepath.Join("lists", name)
	case "scripts":
		path = filepath.Join("lua", name)
	}
	info, err := root.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", os.ErrInvalid
	}
	file, err := root.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var reader io.Reader = file
	if strings.HasSuffix(strings.ToLower(name), ".gz") {
		zipped, err := gzip.NewReader(file)
		if err != nil {
			return "", err
		}
		defer zipped.Close()
		reader = zipped
	}
	const limit = 2 << 20
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return "", err
	}
	if len(data) > limit || !utf8.Valid(data) {
		return "", os.ErrInvalid
	}
	return string(data), nil
}
