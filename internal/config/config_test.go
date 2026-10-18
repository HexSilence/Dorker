package config

import "testing"

func TestValidationMode(t *testing.T) {
	for _, mode := range []string{"off", "conservative", "strict"} {
		c := Default()
		c.Validation.Mode = mode
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", mode, err)
		}
	}
	c := Default()
	c.Validation.Mode = "maybe"
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid mode")
	}
}
func TestUIColorMode(t *testing.T) {
	for _, mode := range []string{"auto", "always", "never"} {
		c := Default()
		c.UI.Color = mode
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	c := Default()
	c.UI.Color = "rainbow"
	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid color mode")
	}
}
