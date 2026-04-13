package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tranchida/gocamel"
)

// headerFileName est la clé du header posé par le composant File.
const headerFileName = "FileName"

// xmlPdfStrategy agrège deux fichiers (XML + PDF) et conserve le contenu XML
// comme corps de l'échange final.
type xmlPdfStrategy struct{}

func (s *xmlPdfStrategy) Aggregate(oldExchange, newExchange *gocamel.Exchange) *gocamel.Exchange {
	if oldExchange == nil {
		return newExchange
	}

	oldName, _ := oldExchange.GetIn().GetHeader(headerFileName)
	newName, _ := newExchange.GetIn().GetHeader(headerFileName)

	// Conserver le corps XML comme corps de l'échange agrégé.
	if strings.HasSuffix(fmt.Sprint(newName), ".xml") {
		oldExchange.GetIn().SetBody(newExchange.GetIn().GetBody())
		oldExchange.GetIn().SetHeader(headerFileName, newName)
	} else if strings.HasSuffix(fmt.Sprint(oldName), ".xml") {
		// Le corps est déjà celui du XML — rien à faire.
	}

	return oldExchange
}

func main() {
	// Répertoire surveillé : répertoire temporaire créé à l'exécution.
	watchDir, err := os.MkdirTemp("", "gocamel-aggregator-*")
	if err != nil {
		fmt.Printf("Erreur création répertoire: %v\n", err)
		return
	}
	defer os.RemoveAll(watchDir)

	fmt.Printf("Répertoire surveillé : %s\n", watchDir)

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("file", gocamel.NewFileComponent())

	// Corrélation : nom de base sans extension (« file.xml » et « file.pdf » → « file »).
	correlationKey := func(exchange *gocamel.Exchange) string {
		name, ok := exchange.GetIn().GetHeader(headerFileName)
		if !ok {
			return "default"
		}
		base := filepath.Base(fmt.Sprint(name))
		return strings.TrimSuffix(base, filepath.Ext(base))
	}

	aggregator := gocamel.NewAggregator(
		correlationKey,
		&xmlPdfStrategy{},
		gocamel.NewMemoryAggregationRepository(),
	).SetCompletionSize(2)

	route := camelCtx.CreateRouteBuilder().
		From("file://"+watchDir+"?include=\\.(xml|pdf)$").
		Aggregate(aggregator).
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			name, _ := exchange.GetIn().GetHeader(headerFileName)
			xmlContent := exchange.GetIn().GetBody()
			fmt.Printf("=== Agrégation complète (fichier XML : %s) ===\n%s\n", name, xmlContent)
			return nil
		}).
		Build()

	camelCtx.AddRoute(route)
	if err := camelCtx.Start(); err != nil {
		fmt.Printf("Erreur démarrage contexte: %v\n", err)
		return
	}
	defer camelCtx.Stop()

	// Laisser le watcher s'initialiser avant d'écrire les fichiers.
	time.Sleep(200 * time.Millisecond)

	// Écriture des deux fichiers déclencheurs.
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<commande>
  <id>42</id>
  <produit>Widget</produit>
  <quantite>10</quantite>
</commande>`

	pdfContent := []byte("%PDF-1.4 fake pdf content")

	if err := os.WriteFile(filepath.Join(watchDir, "file.xml"), []byte(xmlContent), 0644); err != nil {
		fmt.Printf("Erreur écriture file.xml: %v\n", err)
		return
	}
	fmt.Println("file.xml écrit.")

	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(watchDir, "file.pdf"), pdfContent, 0644); err != nil {
		fmt.Printf("Erreur écriture file.pdf: %v\n", err)
		return
	}
	fmt.Println("file.pdf écrit.")

	// Attendre que l'agrégation se complète et que le log s'affiche.
	time.Sleep(time.Second)
}
