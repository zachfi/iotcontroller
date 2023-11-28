package timer

type Config struct {
	// Astro    astro.Config `yaml:"astro"`
	// Named    named.Config `yaml:"named"`
	TimeZone string `yaml:"timezone" json:"timezone"`
}
