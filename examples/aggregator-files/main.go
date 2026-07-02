package main

import (
	"fmt"
	file "gitlab.com/tranchida/gocamel/components/file"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/tranchida/gocamel"
)

// headerFileName is the header key set by the File component.
const headerFileName = "FileName"

// xmlPdfStrategy aggregates two files (XML + PDF) and keeps the XML content
// as the body of the final exchange.
type xmlPdfStrategy struct{}

func (s *xmlPdfStrategy) Aggregate(oldExchange, newExchange *gocamel.Exchange) *gocamel.Exchange {
	if oldExchange == nil {
		return newExchange
	}

	oldName, _ := oldExchange.GetIn().GetHeader(headerFileName)
	newName, _ := newExchange.GetIn().GetHeader(headerFileName)

	// Keep the XML body as the body of the aggregated exchange.
	if strings.HasSuffix(fmt.Sprint(newName), ".xml") {
		oldExchange.GetIn().SetBody(newExchange.GetIn().GetBody())
		oldExchange.GetIn().SetHeader(headerFileName, newName)
	} else if strings.HasSuffix(fmt.Sprint(oldName), ".xml") {
		// The body is already the XML one — nothing to do.
	}

	return oldExchange
}

func main() {
	// Watched directory: temporary directory created at runtime.
	watchDir, err := os.MkdirTemp("", "gocamel-aggregator-*")
	if err != nil {
		fmt.Printf("Error creating directory: %v\n", err)
		return
	}
	defer os.RemoveAll(watchDir)

	fmt.Printf("Watched directory: %s\n", watchDir)

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("file", file.NewFileComponent())

	// Correlation key: base name without extension (e.g. "file.xml" and "file.pdf" → "file").
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

	route, buildErr := camelCtx.CreateRouteBuilder().
		From("file://" + watchDir + "?include=\\.(xml|pdf)$").
		Aggregate(aggregator).
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			name, _ := exchange.GetIn().GetHeader(headerFileName)
			xmlContent := exchange.GetIn().GetBody()
			fmt.Printf("=== Aggregation complete (XML file: %s) ===\n%s\n", name, xmlContent)
			return nil
		}).
		Build()
	if buildErr != nil {
		fmt.Printf("Error creating route: %v\n", buildErr)
		return
	}

	camelCtx.AddRoute(route)
	if err := camelCtx.Start(); err != nil {
		fmt.Printf("Error starting context: %v\n", err)
		return
	}
	defer camelCtx.Stop()

	// Let the watcher initialize before writing files.
	time.Sleep(200 * time.Millisecond)

	// Write the two trigger files.
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<commande>
  <id>42</id>
  <produit>Widget</produit>
  <quantite>10</quantite>
</commande>`

	pdfContent := []byte("%PDF-1.4 fake pdf content")

	if err := os.WriteFile(filepath.Join(watchDir, "file.xml"), []byte(xmlContent), 0644); err != nil {
		fmt.Printf("Error writing file.xml: %v\n", err)
		return
	}
	fmt.Println("file.xml written.")

	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(watchDir, "file.pdf"), pdfContent, 0644); err != nil {
		fmt.Printf("Error writing file.pdf: %v\n", err)
		return
	}
	fmt.Println("file.pdf written.")

	// Wait for the aggregation to complete and the log to appear.
	time.Sleep(time.Second)
}
