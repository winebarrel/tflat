package tflat

// Options is the shared configuration struct for both the library API and the
// CLI. It carries `kong` struct tags so main can mount it directly.
type Options struct {
	Dir       string `arg:"" optional:"" default:"." help:"Root directory containing the .tf files and .terraform/modules. Defaults to '.'."`
	InPlace   bool   `short:"i" help:"Rewrite files in-place instead of printing to stdout."`
	MovedFile string `default:"moved.tf" help:"Filename for the consolidated moved blocks."`
}
