package certs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

func TestValidateCACertificate(t *testing.T) {
	t.Parallel()
	// Initialize logger for testing
	logger.Initialize()

	tests := []struct {
		name        string
		certData    []byte
		wantErr     bool
		errContains string
	}{
		{
			name: "Valid CA certificate",
			certData: []byte(`-----BEGIN CERTIFICATE-----
MIIDfzCCAmegAwIBAgIUBE13KMDSoyh1O0x7PHpV/m0GW7kwDQYJKoZIhvcNAQEL
BQAwTzELMAkGA1UEBhMCVVMxDTALBgNVBAgMBFRlc3QxDTALBgNVBAcMBFRlc3Qx
EDAOBgNVBAoMB1Rlc3QgQ0ExEDAOBgNVBAMMB1Rlc3QgQ0EwHhcNMjUwNTI4MDYx
MTM3WhcNMjYwNTI4MDYxMTM3WjBPMQswCQYDVQQGEwJVUzENMAsGA1UECAwEVGVz
dDENMAsGA1UEBwwEVGVzdDEQMA4GA1UECgwHVGVzdCBDQTEQMA4GA1UEAwwHVGVz
dCBDQTCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAJqIW+I//m/8Yx1z
xdbi6ryHrqiFx07kqBW/RHdLtHD6jGGFuVtbUiKJIZotGmS6d458vU6oayMPXfGR
Vw1nTfWe0ZHKaNC9fnnFZw6nhaWDza7kYN0bhMCGNREqsU674/OTcbKHpIOMjszz
OdaymSyhiGBN1r7wpQS/D82W5L62Ol8f2jrk6CJR9wbQsVkTZkFYsivsINNgsBZ/
rvUxY0LeMZ70lFVWLAjoqias8QH0sjDPfVmHmmani3Vq5wdAdMJ8ZX0XdWhfpRoh
vbYEAnJno1/ao0Jj8kx+4a+vwnFGyUB6gGnR46/S/IyZTweQF60TSwaH2bA4MouF
Qnf9kuUCAwEAAaNTMFEwHQYDVR0OBBYEFHLsXlfUCBKrLdIOQYSKynA9qMALMB8G
A1UdIwQYMBaAFHLsXlfUCBKrLdIOQYSKynA9qMALMA8GA1UdEwEB/wQFMAMBAf8w
DQYJKoZIhvcNAQELBQADggEBAFPZYdu+HTuQdzZaE/0H2wnRbhXldisSMn4z9/3G
zO0LZifnzEtcbXIz2JTmsIVBOBovpjn70F8mR5+tNNMCdgATg6x82TXsu/ymJNV9
hJAGwEzF+U4gjlURVER25QqtPeKXrWVHmcSCYdcS0efpFfmY0tIeMDZvCMEZwk6j
oPRGpNavFD9NEMMVUhMggYk4LAqbaBFCQg2ON4yKkYXPnFe7ap2BWpM23sRBq58L
4CIV1qbg3fjbSxwLQjCN+T+FuucL9Jvswhyl/tCaFYPuMNamXBzLn0uObnjcjvkv
UukCUf8SUaaTa7XF7inVh8cJQYTO1w/QAMJePU6EcxR4Rkc=
-----END CERTIFICATE-----`),
			wantErr: false,
		},
		{
			name: "Valid non-CA certificate (should warn but not fail)",
			certData: []byte(`-----BEGIN CERTIFICATE-----
MIIDezCCAmOgAwIBAgIUHj4jUu5nchjnatnEQkd6jBkHml8wDQYJKoZIhvcNAQEL
BQAwTzELMAkGA1UEBhMCVVMxDTALBgNVBAgMBFRlc3QxDTALBgNVBAcMBFRlc3Qx
EDAOBgNVBAoMB1Rlc3QgQ0ExEDAOBgNVBAMMB1Rlc3QgQ0EwHhcNMjUwNTI4MDYx
MTUwWhcNMjYwNTI4MDYxMTUwWjBcMQswCQYDVQQGEwJVUzENMAsGA1UECAwEVGVz
dDENMAsGA1UEBwwEVGVzdDEUMBIGA1UECgwLVGVzdCBTZXJ2ZXIxGTAXBgNVBAMM
EHRlc3QuZXhhbXBsZS5jb20wggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIB
AQC2vbCff0Od7dk0qS1nAz/jb6DjaDbnUeZUI49NQp2hbUYIRfKf0mrCJ2pIOZyw
gTZz5Q7hJF9A8WY8pVNEvnJtxi4wCC1/+/QtHBPxg0ryJiXAUf9YDMYny8eCfYNQ
I5/VTHIlv+H5DmE+guzX5wAvUmsCFHvd14P0MOS9Hh/sO+ND+xleQ0Occ9kI90UB
ax5/vpq+2Ac16o7LBYIkVEM/AuKQGKfBD8i/V2OK8BDJlXwJ5NSJhyOSDIqG/1qB
D6RDsRG/jk5PTUDKw3FPDC6EX1tRIMwgBk17LWjoHX2tRD3ExthAZqt/d7hDkiaJ
Um+Zxl4+0TWVtHUqn2g9zV3/AgMBAAGjQjBAMB0GA1UdDgQWBBTuPaSgbQrzdlgw
P2U33EztQgovkDAfBgNVHSMEGDAWgBRy7F5X1AgSqy3SDkGEispwPajACzANBgkq
hkiG9w0BAQsFAAOCAQEANmPHd/f0Zw/bGI6zSbutyL20aEQPoaiEo2AVXElYuaK1
bOqK1kBnk64CyBpJ1WJL1ftfTo1BCX8fIeurVeTb2p2Kwet8P51w8pwkpReL7Nv6
Tn/4s3/JYKP+K8Z3/Afw6InZXYwhsha66TniZtJUzPBjj7wrGQNIey7mb502WpNG
inHiCaw+Q9xFLsUNh2Kl2TdMdJM7+AJLpLHrmfJx1jRh9QjMswf/xGQ3CrJTFQ7J
2YPtS8Wbih3+UuyIf0hGG49594quljPfd5bGkH9sK9sIDEbKS0V75mmuFyYMa7qo
mOFFm8Wg1m0OhrYPSUzhWKR6eibMwq9/BTIeSqioSQ==
-----END CERTIFICATE-----`),
			wantErr: false,
		},
		{
			name:        "Empty data",
			certData:    []byte(""),
			wantErr:     true,
			errContains: "no PEM data found",
		},
		{
			name:        "Invalid PEM data",
			certData:    []byte("not a certificate"),
			wantErr:     true,
			errContains: "no PEM data found",
		},
		{
			name: "PEM block but not a certificate",
			certData: []byte(`-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC64TEq9jXHMcXD
gD0ndCex1O2NJyON2pqSWI18d7mKZ9FpukOVdo6Os4ZodbD0JX1bjAIYqMf1p5sF
+jAjajZIrpEFUZx6rYELnS1H9gV1JaI7IOyfRptpDm/OoZA9oG6YOT4gogN/h0Kq
hQEiRN8wgsjj67HpWPIZ4ymPDr6+w/uW27JWp25lwXBPVe4ZcEftQoowGteDlMk+
n1e5LezxCJCMTv5m4Q5CMspb7p4++AxFfX7pa5QsrDBkiSwYLkTm059/lN3AiEyn
UnXfgrqWYFJ9YN3ebbYW41sw3oXPfRKD4eNIrgJZ29ClAgMBAAECggEAQJdwdUFQ
-----END PRIVATE KEY-----`),
			wantErr:     true,
			errContains: "PEM block is not a certificate",
		},
		{
			name: "Invalid certificate data",
			certData: []byte(`-----BEGIN CERTIFICATE-----
aW52YWxpZCBjZXJ0aWZpY2F0ZSBkYXRh
-----END CERTIFICATE-----`),
			wantErr:     true,
			errContains: "failed to parse certificate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateCACertificate(tt.certData)

			if tt.wantErr {
				require.Error(t, err, "ValidateCACertificate() should return an error")
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains, "Error should contain expected substring")
				}
			} else {
				assert.NoError(t, err, "ValidateCACertificate() should not return an error")
			}
		})
	}
}
