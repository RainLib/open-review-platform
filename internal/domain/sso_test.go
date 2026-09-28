package domain

import "testing"

func TestNormalizeSSOConfigurationInput(t *testing.T) {
	oidc, ok := NormalizeSSOConfigurationInput(SSOConfigurationInput{Protocol: SSOProtocolOIDC, DisplayName: "Acme SSO", IssuerURL: "https://id.example.com/", ClientID: "open-review", GroupClaim: "teams", SecretRef: "secret://sso/acme"})
	if !ok || oidc.IssuerURL != "https://id.example.com" {
		t.Fatalf("oidc=%#v ok=%v", oidc, ok)
	}
	if _, ok := NormalizeSSOConfigurationInput(SSOConfigurationInput{Protocol: SSOProtocolOIDC, DisplayName: "Acme", IssuerURL: "http://metadata.internal", ClientID: "client"}); ok {
		t.Fatal("plain HTTP issuer must be rejected")
	}
	if _, ok := NormalizeSSOConfigurationInput(SSOConfigurationInput{Protocol: SSOProtocolSAML, DisplayName: "Acme", MetadataURL: "https://id.example.com/metadata", ClientID: "unexpected"}); ok {
		t.Fatal("SAML input must reject OIDC-only fields")
	}
}

func TestNormalizeSSODomainAndMapping(t *testing.T) {
	if domain, ok := NormalizeSSODomain(" Engineering.Example.COM. "); !ok || domain != "engineering.example.com" {
		t.Fatalf("domain=%q ok=%v", domain, ok)
	}
	if _, ok := NormalizeSSODomain("localhost"); ok {
		t.Fatal("single-label domains must be rejected")
	}
	mapping, ok := NormalizeSSORoleMappingInput(SSORoleMappingInput{GroupValue: "platform-admins", Role: "admin"})
	if !ok || mapping.RepositoryScope != "*" {
		t.Fatalf("mapping=%#v ok=%v", mapping, ok)
	}
}
