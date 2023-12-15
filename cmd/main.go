package main

import (
	"log"

	watchgoissues "github.com/j178/watch-go-issues/watch"
)

func main() {
	err := watchgoissues.Watch()
	if err != nil {
		log.Fatalln(err)
	}
}
