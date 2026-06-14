package config

import "testing"

// TestValidateSecrets ensures the bot refuses to boot with an empty, default, or
// too-short JWT secret (which would make every issued token forgeable) and
// accepts a strong one.
func TestValidateSecrets(t *testing.T) {
	orig := JWTSecret
	defer func() { JWTSecret = orig }()

	cases := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{"empty", "", true},
		{"default placeholder", defaultJWTSecret, true},
		{"too short", "short-secret", true},
		{"strong", "a-sufficiently-long-random-production-secret-value", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			JWTSecret = tc.secret
			err := ValidateSecrets()
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateSecrets() = nil, want error for %q", tc.secret)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateSecrets() = %v, want nil", err)
			}
		})
	}
}
