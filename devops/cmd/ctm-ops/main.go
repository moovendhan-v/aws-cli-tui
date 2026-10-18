// Command ctm-ops is a keyboard-driven AWS DevOps terminal.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/app"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/preview"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui"
)

var version = "0.1.0-dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	check := flag.Bool("check", false, "validate configuration and command definitions, then exit")
	profile := flag.String("profile", "", "AWS profile to start with (overrides AWS_PROFILE)")
	region := flag.String("region", "", "AWS region to start with")
	showImage := flag.String("show-image", "", "internal: show an image file full screen (used by S3 previews)")
	flag.Parse()

	if *showImage != "" {
		if err := preview.Show(*showImage, os.Stdout, os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, "ctm-ops:", err)
			os.Exit(1)
		}
		return
	}

	if *showVersion {
		fmt.Println("ctm-ops", version)
		return
	}

	deps, err := app.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ctm-ops:", err)
		os.Exit(1)
	}
	defer deps.Close()
	if *profile != "" {
		deps.Config.UI.DefaultProfile = *profile
	}
	if *region != "" {
		deps.Config.UI.DefaultRegion = *region
	}

	if *check {
		os.Exit(runCheck(deps))
	}
	if err := tui.Run(deps); err != nil {
		fmt.Fprintln(os.Stderr, "ctm-ops:", err)
		os.Exit(1)
	}
}

func runCheck(d *app.Deps) int {
	fmt.Printf("config:    %s\n", d.Config.Path())
	fmt.Printf("database:  %s\n", d.DBPath)
	fmt.Printf("profiles:  %d\n", len(d.Profiles))
	cats := d.Registry.Categories()
	names := make([]string, 0, len(cats))
	for c := range cats {
		names = append(names, c)
	}
	sort.Strings(names)
	fmt.Printf("commands:  %d registered\n", len(d.Registry.All()))
	for _, c := range names {
		fmt.Printf("  %-12s %d\n", c, cats[c])
	}
	for _, w := range d.Warnings {
		fmt.Println("warning:", w)
	}
	if len(d.Registry.Rejected) > 0 {
		fmt.Printf("rejected:  %d\n", len(d.Registry.Rejected))
		for _, r := range d.Registry.Rejected {
			fmt.Println("  ✗", r.Error())
		}
		return 1
	}
	fmt.Println("ok")
	return 0
}
