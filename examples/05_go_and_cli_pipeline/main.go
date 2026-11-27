package main

import (
	"fmt"
	"log"
	"runtime"
	"sync"

	"github.com/DimaKorshikov/rulemma/embedded"
)

func main() {
	lem, err := embedded.Load()
	if err != nil {
		log.Fatal(err)
	}

	queries := []string{
		"Купить зимние кроссовки Найк",
		"Кроссовки на зиму Найк недорого",
		"Шина 35 см для бензопилы",
		"Витамин Е 400 мг в капсулах",
		"Встраиваемые посудомоечные машины 60 см",
		"Оцинкованные ведра 10 л",
	}

	workers := runtime.NumCPU()
	if workers > len(queries) {
		workers = len(queries)
	}

	results := make([]string, len(queries))
	jobs := make(chan int, len(queries))
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				// Lemmatizer безопасен для конкурентного чтения без мьютексов
				results[idx] = lem.SemanticKey(queries[idx])
			}
		}()
	}

	for i := range queries {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	if results[0] != results[1] {
		log.Fatalf("expected identical semantic keys, got %q vs %q", results[0], results[1])
	}
	for i, q := range queries {
		fmt.Printf("%-42s => %s\n", q, results[i])
	}
}
