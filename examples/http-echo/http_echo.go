package main

import (
	"fmt"
	"time"

	"gitlab.com/tranchida/gocamel"
)

func main() {

	context := gocamel.NewCamelContext()

	context.AddComponent("http", gocamel.NewHTTPComponent())

	// Création d'une route qui écoute sur le port 8080 et renvoie le message reçu
	route, err := context.CreateRouteBuilder().
		From("http://localhost:8080/echo").
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			// Ajout d'un en-tête de réponse
			exchange.SetHeader("X-Processed-At", time.Now().Format(time.RFC3339))
			//exchange.GetOut().SetBody(exchange.GetIn().GetBody())
			return nil
		}).Build()
	if err != nil {
		fmt.Printf("Erreur création route: %v\n", err)
		return
	}

	context.AddRoute(route)
	context.Start()

	fmt.Println("Serveur démarré sur http://localhost:8080/echo")
	fmt.Println("Appuyez sur Ctrl+C pour arrêter")

	// Attente indéfinie
	select {}
}
