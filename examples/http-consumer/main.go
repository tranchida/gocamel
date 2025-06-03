package main

import (
	"fmt"
	"time"

	"github.com/tranchida/gocamel"
)

func main() {
	context := gocamel.NewCamelContext()

	// Enregistrement du composant HTTP
	context.AddComponent("http", gocamel.NewHTTPComponent())

	// Création d'une route qui écoute sur le port 8080 et renvoie le message reçu
	route := context.CreateRouteBuilder().
		From("http://localhost:8080/echo").
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			// Ajout d'un en-tête de réponse
			exchange.GetOut().SetBody("Hello, World!")
			exchange.GetOut().SetHeader("Content-Type", "text/plain")
			exchange.GetOut().SetHeader("Status-Code", "200")
			exchange.GetOut().SetHeader("X-Processed-At", time.Now().Format(time.RFC3339))
			return nil
		}).
		Build()

	context.AddRoute(route)
	context.Start()

	fmt.Println("Serveur démarré sur http://localhost:8080/echo")
	fmt.Println("Appuyez sur Ctrl+C pour arrêter")

	// Attente indéfinie
	select {}
}
