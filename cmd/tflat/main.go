package main

import (
	"log"
	"os"

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
		if err := res.WriteToDir(opts.Dir); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := res.WriteToStdout(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
