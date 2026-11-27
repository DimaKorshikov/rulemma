package main

import (
	"fmt"
	"log"

	"github.com/DimaKorshikov/rulemma"
	"github.com/DimaKorshikov/rulemma/embedded"
)

func main() {
	// Подключение словарей в одну строку через //go:embed (без ручного пути к data/)
	lem, err := embedded.Load()
	if err != nil {
		log.Fatal(err)
	}

	words := [][2]string{
		{"кроссовки", "кроссовок"},
		{"чёрные", "черный"},
		{"роботов-пылесосов", "робот-пылесос"},
		{"отпариватели", "отпариватель"},
		{"см", "см"},
	}
	for _, tc := range words {
		got := lem.Word(tc[0])
		if got != tc[1] {
			log.Fatalf("Word(%q) = %q, want %q", tc[0], got, tc[1])
		}
		fmt.Printf("  %-20s -> %s\n", tc[0], got)
	}

	phrase := "Купить чёрные роботы-пылесосы, 35 см!"
	fmt.Println("NormalizePhrase:", rulemma.NormalizePhrase(phrase, false))
	fmt.Println("LemmatizeString:", lem.LemmatizeString(phrase))
}
