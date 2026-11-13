package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"wowcensuscompanion/companion"
)

func main() {
	appDir := flag.String("app-dir", "", "config/state directory (default: OS user config dir)")
	scan := flag.Bool("scan", false, "scan once, print detected files and exit")
	report := flag.Bool("report", false, "print the maintenance report and exit")
	uploadNow := flag.Bool("upload-now", false, "upload current files once (registers automatically) and exit")
	backupNow := flag.Bool("backup-now", false, "write backups once and exit")
	flag.Parse()

	e, err := companion.NewEngine(*appDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	var printed int64
	e.OnChange(func(s companion.Status) {
		for _, l := range s.Log {
			if l.Seq > printed {
				printed = l.Seq
				fmt.Printf("%s [%s] %s\n", l.At.Local().Format("15:04:05"), l.Level, l.Message)
			}
		}
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch {
	case *scan, *report, *uploadNow, *backupNow:
		e.RunOnce(ctx, *uploadNow, *backupNow)
		if *report {
			printJSON(e.Report())
		} else {
			printJSON(e.Status().Files)
		}
	default:
		fmt.Println("Config and state:", e.AppDir())
		e.Run(ctx)
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
