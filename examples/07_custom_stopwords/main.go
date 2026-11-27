package main

import (
	"fmt"
	"log"

	"github.com/DimaKorshikov/rulemma"
	"github.com/DimaKorshikov/rulemma/embedded"
)

func main() {
	lem, err := embedded.Load()
	if err != nil {
		log.Fatal(err)
	}

	query := "Купить зимние кроссовки Найк в Москве дёшево"
	customStop := rulemma.MakeStopWords([]string{"в", "москва", "дёшево"})

	defaultKey := lem.LemmaKey(query)
	customKey := lem.LemmaKeyWithStop(query, customStop)
	semKey := lem.SemanticKeyWithStop(query, customStop)

	if customKey != "зимний кроссовок купить найк" {
		log.Fatalf("unexpected customKey: %q", customKey)
	}
	if semKey != "зима кроссовок купить найк" {
		log.Fatalf("unexpected semKey: %q", semKey)
	}

	fmt.Println("Default LemmaKey:", defaultKey)
	fmt.Println("Custom  LemmaKey:", customKey)
	fmt.Println("Custom  SemKey:  ", semKey)
}
