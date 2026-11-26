package identity

// SocialRegistry holds the providers that are switched on. cmd registers only providers with credentials, so a
// provider without keys is neither offered nor reachable.
type SocialRegistry struct {
	order []SocialProvider
	byKey map[string]SocialProvider
}

// NewSocialRegistry returns a registry of the providers, listed in the order given.
func NewSocialRegistry(providers ...SocialProvider) *SocialRegistry {
	r := &SocialRegistry{byKey: make(map[string]SocialProvider, len(providers))}
	for _, p := range providers {
		r.order = append(r.order, p)
		r.byKey[p.Code()] = p
	}
	return r
}

// Find returns the provider with the code.
func (r *SocialRegistry) Find(code string) (SocialProvider, bool) {
	p, ok := r.byKey[code]
	return p, ok
}

// Enabled lists the providers in registration order.
func (r *SocialRegistry) Enabled() []SocialProvider { return r.order }
