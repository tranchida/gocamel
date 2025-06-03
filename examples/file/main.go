package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tranchida/gocamel"
)

func main() {
	// Création d'un répertoire temporaire pour les tests
	tempDir, err := os.MkdirTemp("", "gocamel-test-*")
	if err != nil {
		fmt.Printf("Erreur lors de la création du répertoire temporaire: %v\n", err)
		return
	}
	defer os.RemoveAll(tempDir)

	// Création du contexte et enregistrement du composant File
	context := gocamel.NewCamelContext()
	context.AddComponent("file", gocamel.NewFileComponent())

	// Création d'une route qui surveille le répertoire
	route := context.CreateRouteBuilder().
		From("file://" + tempDir).
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			fmt.Printf("Nouveau fichier détecté:\n")
			if fileName, ok := exchange.GetIn().GetHeader("CamelFileName"); ok {
				fmt.Printf("  Nom: %s\n", fileName)
			}
			if filePath, ok := exchange.GetIn().GetHeader("CamelFileAbsolutePath"); ok {
				fmt.Printf("  Chemin: %s\n", filePath)
			}
			fmt.Printf("  Contenu: %s\n", exchange.GetIn().GetBody())
			return nil
		}).
		Build()

	// Démarrage de la route
	context.AddRoute(route)
	context.Start()

	fmt.Printf("Surveillance du répertoire: %s\n", tempDir)

	// Création de quelques fichiers de test
	for i := 1; i <= 3; i++ {
		content := fmt.Sprintf("Contenu du fichier test %d", i)
		filename := filepath.Join(tempDir, fmt.Sprintf("test%d.txt", i))
		if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
			fmt.Printf("Erreur lors de l'écriture du fichier: %v\n", err)
			return
		}
		time.Sleep(time.Second)
	}

	// Attente pour voir les résultats
	time.Sleep(2 * time.Second)
}
