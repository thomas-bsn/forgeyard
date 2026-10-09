// Package dns creates the DNS records of apps through the API of the domain's DNS provider, using the
// libdns provider implementations (the ones Caddy uses).
package dns

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/libdns/cloudflare"
	"github.com/libdns/gandi"
	"github.com/libdns/libdns"
	"github.com/libdns/ovh"
	"github.com/libdns/porkbun"
)

// Provider is what Forgeyard needs from a DNS provider.
type Provider interface {
	libdns.RecordGetter
	libdns.RecordSetter
	libdns.RecordDeleter
}

// Field is a credential the user pastes from their provider.
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Secret      bool   `json:"secret"`
	Placeholder string `json:"placeholder,omitempty"`
}

// Kind describes a supported provider.
type Kind struct {
	Name    string  `json:"name"`
	Label   string  `json:"label"`
	DocsURL string  `json:"docsUrl"`
	Help    string  `json:"help"`
	Fields  []Field `json:"fields"`
	build   func(creds map[string]string) Provider
}

// Kinds are the supported providers, in the order shown to users.
var Kinds = []Kind{
	{
		Name: "cloudflare", Label: "Cloudflare", DocsURL: "https://dash.cloudflare.com/profile/api-tokens",
		Help:   "Créez un token avec le modèle « Edit zone DNS », limité à votre domaine.",
		Fields: []Field{{Key: "api_token", Label: "Token API", Secret: true}},
		build:  func(c map[string]string) Provider { return &cloudflare.Provider{APIToken: c["api_token"]} },
	},
	{
		Name: "ovh", Label: "OVHcloud", DocsURL: "https://eu.api.ovh.com/createToken/",
		Help: "Créez une clé avec les droits GET, POST, PUT et DELETE sur /domain/zone/*.",
		Fields: []Field{
			{Key: "endpoint", Label: "Endpoint", Placeholder: "ovh-eu"},
			{Key: "application_key", Label: "Application key"},
			{Key: "application_secret", Label: "Application secret", Secret: true},
			{Key: "consumer_key", Label: "Consumer key", Secret: true},
		},
		build: func(c map[string]string) Provider {
			return &ovh.Provider{Endpoint: c["endpoint"], ApplicationKey: c["application_key"],
				ApplicationSecret: c["application_secret"], ConsumerKey: c["consumer_key"]}
		},
	},
	{
		Name: "gandi", Label: "Gandi", DocsURL: "https://account.gandi.net/",
		Help:   "Créez un jeton d'accès personnel avec le droit « Gérer la configuration technique des domaines ».",
		Fields: []Field{{Key: "bearer_token", Label: "Jeton d'accès personnel", Secret: true}},
		build:  func(c map[string]string) Provider { return &gandi.Provider{BearerToken: c["bearer_token"]} },
	},
	{
		Name: "porkbun", Label: "Porkbun", DocsURL: "https://porkbun.com/account/api",
		Help: "Créez une clé API, puis activez « API Access » sur le domaine.",
		Fields: []Field{
			{Key: "api_key", Label: "API key"},
			{Key: "api_secret_key", Label: "Secret API key", Secret: true},
		},
		build: func(c map[string]string) Provider {
			return &porkbun.Provider{APIKey: c["api_key"], APISecretKey: c["api_secret_key"]}
		},
	},
}

// Lookup returns the provider kind with this name.
func Lookup(name string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

// New returns a provider client. Every field of the kind must be set.
func New(name string, creds map[string]string) (Provider, error) {
	k, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("fournisseur DNS inconnu : %s", name)
	}
	for _, f := range k.Fields {
		if strings.TrimSpace(creds[f.Key]) == "" {
			return nil, fmt.Errorf("%s : le champ « %s » est obligatoire", k.Label, f.Label)
		}
	}
	return k.build(creds), nil
}

// recordTTL is short so moving an app to another node takes effect quickly.
const recordTTL = 5 * time.Minute

// FindZone returns the zone holding domain ("example.com." for "apps.example.com"): the domain itself or
// its closest parent that the credentials can read.
func FindZone(ctx context.Context, p Provider, domain string) (string, error) {
	labels := strings.Split(strings.Trim(strings.ToLower(domain), "."), ".")
	var lastErr error
	for i := 0; i+1 < len(labels); i++ {
		zone := strings.Join(labels[i:], ".") + "."
		if _, err := p.GetRecords(ctx, zone); err == nil {
			return zone, nil
		} else {
			lastErr = err
		}
	}
	return "", fmt.Errorf("aucune zone accessible avec ces identifiants ne contient %s (%v)", domain, lastErr)
}

// SetAddress points name (a full domain name in zone) to ip, replacing any previous A/AAAA value.
func SetAddress(ctx context.Context, p Provider, zone, name string, ip netip.Addr) error {
	_, err := p.SetRecords(ctx, zone, []libdns.Record{libdns.Address{
		Name: libdns.RelativeName(name, zone), TTL: recordTTL, IP: ip,
	}})
	return err
}

// DeleteAddress removes the A or AAAA records of name. A missing record is not an error.
func DeleteAddress(ctx context.Context, p Provider, zone, name string, ip netip.Addr) error {
	_, err := p.DeleteRecords(ctx, zone, []libdns.Record{libdns.Address{
		Name: libdns.RelativeName(name, zone), IP: ip,
	}})
	return err
}

// Exists reports whether name (a full domain name in zone) already has an A, AAAA or CNAME record.
func Exists(ctx context.Context, p Provider, zone, name string) (bool, error) {
	recs, err := p.GetRecords(ctx, zone)
	if err != nil {
		return false, err
	}
	rel := libdns.RelativeName(name, zone)
	for _, r := range recs {
		rr := r.RR()
		if rr.Name == rel && (rr.Type == "A" || rr.Type == "AAAA" || rr.Type == "CNAME") {
			return true, nil
		}
	}
	return false, nil
}
