package aes_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stacklok/toolhive/pkg/kubernetes/secrets/aes"
)

func TestGCMEncrypt(t *testing.T) {
	t.Parallel()

	scenarios := []struct {
		Name          string
		Key           []byte
		Plaintext     []byte
		ExpectedError string
	}{
		{
			Name:          "GCM Encrypt rejects short key",
			Key:           []byte{0x41, 0x42, 0x43, 0x44},
			Plaintext:     []byte(plaintext),
			ExpectedError: "invalid key size",
		},
		{
			Name:          "GCM Encrypt rejects oversized plaintext",
			Key:           key,
			Plaintext:     make([]byte, 33*1024*1024), // 33MiB
			ExpectedError: aes.ErrExceedsMaxSize.Error(),
		},
		{
			Name:      "GCM encrypts plaintext",
			Key:       key,
			Plaintext: []byte(plaintext),
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			t.Parallel()

			result, err := aes.Encrypt(scenario.Plaintext, scenario.Key)
			if scenario.ExpectedError == "" {
				require.NoError(t, err)
				// validate by decrypting
				decrypted, err := aes.Decrypt(result, key)
				require.NoError(t, err)
				require.Equal(t, scenario.Plaintext, decrypted)
			} else {
				require.ErrorContains(t, err, scenario.ExpectedError)
			}
		})
	}
}

// This doesn't test decryption - that is tested in the happy path of the encrypt test
func TestGCMDecrypt(t *testing.T) {
	t.Parallel()

	scenarios := []struct {
		Name          string
		Key           []byte
		Ciphertext    []byte
		ExpectedError string
	}{
		{
			Name:          "GCM Decrypt rejects short key",
			Key:           []byte{0xa},
			Ciphertext:    []byte(plaintext),
			ExpectedError: "invalid key size",
		},
		{
			Name:          "GCM Decrypt rejects malformed ciphertext",
			Key:           key,
			Ciphertext:    make([]byte, 32), // 33MiB
			ExpectedError: "message authentication failed",
		},
		{
			Name:          "GCM Decrypt rejects undersized ciphertext",
			Key:           key,
			Ciphertext:    []byte{0xFF},
			ExpectedError: "malformed ciphertext",
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			t.Parallel()

			_, err := aes.Decrypt(scenario.Ciphertext, scenario.Key)
			require.ErrorContains(t, err, scenario.ExpectedError)
		})
	}
}

var key = []byte{0x7a, 0x91, 0xc8, 0x36, 0x47, 0xdf, 0xe2, 0x0b, 0x3d, 0x8c, 0x57, 0xf8, 0x15, 0xae, 0x69, 0x02, 0xc4,
	0x5f, 0xba, 0x83, 0x1e, 0x70, 0x96, 0xd1, 0x4c, 0x25, 0xa7, 0xf3, 0x6d, 0x08, 0xe9, 0xb4}

const plaintext = "Hello world"
