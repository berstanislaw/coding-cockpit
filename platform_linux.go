package main

import "os/exec"

func openURL(url string) {
	go exec.Command("xdg-open", url).Run()
}

func notify(title, body string) {
	exec.Command("notify-send", "-a", "kta", title, body).Run()
}
