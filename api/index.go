package api

import (
	"net/http"
	"os"

	watchgoissues "github.com/j178/watch-go-issues/watch"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	secret := r.Header.Get("Authorization")
	if secret != os.Getenv("SECRET") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	err := watchgoissues.Watch()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
