package main

import (
	"context"
	"log"
)

func main() {
	if err := Run(context.Background()); err != nil {
		log.Fatalf("seed: %v", err)
	}
	log.Println("seed: ok")
}
