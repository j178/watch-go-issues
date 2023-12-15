package api

import (
	"net/http"

	watchgoissues "github.com/j178/watch-go-issues"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	err := watchgoissues.Watch()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
