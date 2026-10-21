package update

import "os/exec"

func osExecutableLookPath(name string) (string, error) { return exec.LookPath(name) }
