package pwobservability

import (
	"testing"

	"github.com/shibukawa/popcornweb/pwconfig"
)

func TestResolveSamplerDefaultsByEnvironment(t *testing.T) {
	for _, testCase := range []struct{ env, want string }{
		{env: pwconfig.EnvDevelopment, want: "parentbased_always_on"},
		{env: pwconfig.EnvProduction, want: "parentbased_traceidratio{0.1}"},
	} {
		sampler, err := ResolveSampler(pwconfig.ObservabilityConfig{}, testCase.env)
		if err != nil {
			t.Fatalf("%s: %v", testCase.env, err)
		}
		if got := sampler.Description(); got != testCase.want {
			t.Errorf("%s: sampler = %q, want %q", testCase.env, got, testCase.want)
		}
	}
}

func TestResolveSamplerPrefersTheConfiguredValue(t *testing.T) {
	config := pwconfig.ObservabilityConfig{}
	config.Trace.Sampler = "traceidratio"
	config.Trace.SamplerArg = "0.25"
	for _, env := range []string{pwconfig.EnvDevelopment, pwconfig.EnvProduction} {
		sampler, err := ResolveSampler(config, env)
		if err != nil {
			t.Fatalf("%s: %v", env, err)
		}
		if got := sampler.Description(); got != "traceidratio{0.25}" {
			t.Errorf("%s: sampler = %q, want the configured one", env, got)
		}
	}
}

func TestResolveSamplerReportsAnUnusableValue(t *testing.T) {
	config := pwconfig.ObservabilityConfig{}
	config.Trace.Sampler = "traceidratio"
	config.Trace.SamplerArg = "most of them"
	if _, err := ResolveSampler(config, pwconfig.EnvProduction); err == nil {
		t.Fatal("an unparseable sampler argument was accepted")
	}
}
