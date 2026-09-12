package pwconfig

import "testing"

// restoreEnvState puts the process-wide environment answer back, so one test
// cannot decide another one's relaxations.
func restoreEnvState(t *testing.T) {
	t.Helper()
	envState.RLock()
	value, declared, known := envState.value, envState.declared, envState.known
	envState.RUnlock()
	t.Cleanup(func() {
		envState.Lock()
		envState.value, envState.declared, envState.known = value, declared, known
		envState.Unlock()
	})
}

// An unset APP_ENV is development. Running with no environment set is what
// working on an application looks like, and refusing it would fail the case the
// default exists to serve.
func TestAnUnnamedEnvironmentIsDevelopment(t *testing.T) {
	restoreEnvState(t)
	setEnv(DefaultEnv, false)

	if !Development() {
		t.Error("an unset APP_ENV should keep the development relaxations")
	}
	if EnvironmentDeclared() {
		t.Error("an unset APP_ENV should not count as declared")
	}
}

// A named development environment is the same answer, arrived at deliberately.
func TestANamedDevelopmentEnvironmentIsDevelopment(t *testing.T) {
	restoreEnvState(t)
	setEnv(EnvDevelopment, true)

	if !Development() {
		t.Error("APP_ENV=dev should keep the development relaxations")
	}
	if !EnvironmentDeclared() {
		t.Error("APP_ENV=dev should count as declared")
	}
}

// Every other named environment loses them. The check used to be a list of the
// environments it refused — "stg", "prod", "production" — so "staging", "prd",
// "live" and every other spelling walked past a lock built to stop exactly them.
func TestANamedNonDevelopmentEnvironmentLosesTheRelaxations(t *testing.T) {
	for _, environment := range []string{EnvStaging, "prod-eu"} {
		t.Run(environment, func(t *testing.T) {
			restoreEnvState(t)
			setEnv(environment, true)

			if Development() {
				t.Errorf("APP_ENV=%q kept the development relaxations", environment)
			}
			if !EnvironmentDeclared() {
				t.Errorf("APP_ENV=%q should count as declared", environment)
			}
		})
	}
}
