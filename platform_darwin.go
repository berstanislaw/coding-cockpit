package main

import "os/exec"

func openURL(url string) {
	go exec.Command("open", url).Run()
}

func notify(title, body string) {
	exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, body).Run()
}
