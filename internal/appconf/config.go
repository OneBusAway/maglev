package appconf

// Config holds all the configuration settings for our Application.
// For now, the only configuration settings will be the network port that we want the
// server to listen on, and the name of the current operating environment for the
// Application (development, staging, production, etc.). We will read in these
// configuration settings from command-line flags when the Application starts.
type Config struct {
	Port          int
	Env           Environment
	ApiKeys       []string
	ExemptApiKeys []string
	Verbose       bool
	RateLimit     int // Requests per second per API key for rate limiting
}

// Environment is an enumerated type representing various stages or configurations in the system's lifecycle.
type Environment int

// Environment constants
const (
	Development Environment = iota // 0
	Test                           // 1
	Production                     // 2
)

func EnvFlagToEnvironment(envFlag string) Environment {
	switch envFlag {
	case "development":
		return Development
	case "test":
		return Test
	case "production":
		return Production
	default:
		return Development
	}
}
