package main

import (
	"fmt"

	huhspinner "github.com/charmbracelet/huh/spinner"
)

// runWithSpinner runs a function with an animated spinner.
// Shows a success or failure message when done.
func runWithSpinner(title string, fn func() error) error {
	var fnErr error
	err := huhspinner.New().
		Title(title).
		Action(func() {
			fnErr = fn()
		}).
		Run()
	if err != nil {
		return err
	}
	if fnErr != nil {
		fmt.Println(fail(fmt.Sprintf("%s: %v", title, fnErr)))
		return fnErr
	}
	fmt.Println(pass(title))
	return nil
}
