package shell

import (
	"os/exec"
)

func OpenURL(url string) {
	go exec.Command("xdg-open", url).Run()
}

func Notify(title, body string) {
	exec.Command("notify-send", "-a", "kta", title, body).Run()
}
