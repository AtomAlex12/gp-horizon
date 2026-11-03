package api

import (
	"errors"
	"net/http"
	"os"

	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/nfqws"
)

func (d Deps) handleNfqwsFiles(w http.ResponseWriter, r *http.Request) {
	if d.Engines == nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := d.Engines.Get(engine.KindNfqws2); !ok {
		http.Error(w, "NFQWS2 не подключён", http.StatusNotFound)
		return
	}
	const dir = "/opt/etc/nfqws2"
	section := r.URL.Query().Get("section")
	if section == "interfaces" && d.Keenetic != nil {
		interfaces, err := d.Keenetic.Interfaces(r.Context())
		if err != nil {
			http.Error(w, "Данные Keenetic недоступны", http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, interfaces)
		return
	}
	name := r.URL.Query().Get("name")
	var result any
	var err error
	if name == "" {
		result, err = nfqws.Files(dir, section)
	} else {
		var text string
		text, err = nfqws.ReadFile(dir, section, name)
		result = map[string]string{"text": text}
	}
	switch {
	case err == nil:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, result)
	case errors.Is(err, os.ErrNotExist):
		http.Error(w, "Файл или каталог не найден", http.StatusNotFound)
	case errors.Is(err, os.ErrInvalid):
		http.Error(w, "Недопустимый файл или каталог", http.StatusBadRequest)
	default:
		http.Error(w, "Не удалось прочитать данные NFQWS2", http.StatusInternalServerError)
	}
}
