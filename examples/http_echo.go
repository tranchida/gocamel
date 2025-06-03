package http_echo

import (
	"fmt"
	"time"

	"github.com/tranchida/gocamel"
)

func main() {

	context := gocamel.NewCamelContext()
	// Création d'une route qui écoute sur le port 8080 et renvoie le message reçu
	route := gocamel.NewRoute()
	route.From("http://localhost:8080/echo").
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			// Ajout d'un en-tête de réponse
			exchange.SetHeader("X-Processed-At", time.Now().Format(time.RFC3339))
			return nil
		})

	context.AddRoute(route)
	context.Start()

	fmt.Println("Serveur démarré sur http://localhost:8080/echo")
	fmt.Println("Appuyez sur Ctrl+C pour arrêter")

	// Attente indéfinie
	select {}
}
