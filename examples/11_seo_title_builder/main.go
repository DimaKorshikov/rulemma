package main

import (
	"fmt"
	"log"

	"github.com/DimaKorshikov/rulemma/embedded"
)

func main() {
	lem, err := embedded.Load()
	if err != nil {
		log.Fatal(err)
	}

	seo, added, agreed := lem.BuildSEOName(
		"Ведро строительное",
		"Ведро",
		[]string{"строительный", "оцинкованные", "большие"},
		100,
	)
	if seo != "Ведро строительное, оцинкованное, большое" || added != 2 || agreed != 2 {
		log.Fatalf("unexpected BuildSEOName: seo=%q added=%d agreed=%d", seo, added, agreed)
	}
	fmt.Printf("SEO-название: %s (добавлено=%d, согласовано=%d)\n", seo, added, agreed)
}
