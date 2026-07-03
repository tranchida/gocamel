package main

import (
	"fmt"
	file "gitlab.com/tranchida/gocamel/components/file"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/tranchida/gocamel"
)

func main() {
	// Create a temporary directory for testing.
	tempDir, err := os.MkdirTemp("", "gocamel-test-*")
	if err != nil {
		fmt.Printf("Error creating temporary directory: %v\n", err)
		return
	}
	defer os.RemoveAll(tempDir)

	// Create the context and register the File component.
	context := gocamel.NewCamelContext()
	context.AddComponent("file", file.NewFileComponent())

	// Create a route that watches the directory.
	route, err := context.CreateRouteBuilder().
		From("file://" + tempDir).
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			fmt.Printf("New file detected:\n")
			if fileName, ok := exchange.GetIn().GetHeader("CamelFileName"); ok {
				fmt.Printf("  Name: %s\n", fileName)
			}
			if filePath, ok := exchange.GetIn().GetHeader("CamelFileAbsolutePath"); ok {
				fmt.Printf("  Path: %s\n", filePath)
			}
			fmt.Printf("  Content: %s\n", exchange.GetIn().GetBody())
			return nil
		}).
		Build()
	if err != nil {
		fmt.Printf("Error creating route: %v\n", err)
		return
	}

	// Start the route.
	context.AddRoute(route)
	context.Start()

	fmt.Printf("Watching directory: %s\n", tempDir)

	// Create a few test files.
	for i := 1; i <= 3; i++ {
		content := fmt.Sprintf("Content of test file %d", i)
		filename := filepath.Join(tempDir, fmt.Sprintf("test%d.txt", i))
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			fmt.Printf("Error writing file: %v\n", err)
			return
		}
		time.Sleep(time.Second)
	}

	// Wait to see the results.
	time.Sleep(2 * time.Second)
}
