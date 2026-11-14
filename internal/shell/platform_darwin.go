package shell

import "os/exec"

func OpenURL(url string) {
	go exec.Command("open", url).Run()
}

func Notify(title, body string) {
	exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, body).Run()
}
