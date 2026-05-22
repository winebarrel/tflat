package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/winebarrel/tflat"
)

var version string

func init() {
	log.SetFlags(0)
}

type cli struct {
	tflat.Options
	Version kong.VersionFlag
}

func main() {
	opts := &cli{}
	parser := kong.Must(opts, kong.Vars{"version": version})
	parser.Model.HelpFlag.Help = "Show help."
	if _, err := parser.Parse(os.Args[1:]); err != nil {
		parser.FatalIfErrorf(err)
	}

	res, err := tflat.Flatten(&opts.Options)
	if err != nil {
		log.Fatal(err)
	}

	if opts.InPlace {
		for _, f := range res.Files {
			path := filepath.Join(opts.Dir, f.Path)
			if err := os.WriteFile(path, f.Content, 0644); err != nil {
				log.Fatal(err)
			}
		}
		return
	}

	for i, f := range res.Files {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("# === %s ===\n", f.Path)
		os.Stdout.Write(f.Content)
	}
}
