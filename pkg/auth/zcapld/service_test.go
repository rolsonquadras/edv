/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package zcapld

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hyperledger/aries-framework-go/pkg/doc/ld"
	mockcrypto "github.com/hyperledger/aries-framework-go/pkg/mock/crypto"
	mockkms "github.com/hyperledger/aries-framework-go/pkg/mock/kms"
	mockldstore "github.com/hyperledger/aries-framework-go/pkg/mock/ld"
	mockstorage "github.com/hyperledger/aries-framework-go/pkg/mock/storage"
	ldstore "github.com/hyperledger/aries-framework-go/pkg/store/ld"
	"github.com/square/go-jose/json"
	"github.com/stretchr/testify/require"
	"github.com/trustbloc/edge-core/pkg/zcapld"
)

func TestNew(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc, err := New(&mockkms.KeyManager{},
			&mockcrypto.Crypto{},
			mockstorage.NewMockStoreProvider(),
			createTestDocumentLoader(t),
			nil,
		)
		require.NoError(t, err)
		require.NotNil(t, svc)
	})

	t.Run("success", func(t *testing.T) {
		svc, err := New(&mockkms.KeyManager{},
			&mockcrypto.Crypto{},
			&mockstorage.MockStoreProvider{ErrOpenStoreHandle: fmt.Errorf("failed to open")},
			createTestDocumentLoader(t),
			nil,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to open")
		require.Nil(t, svc)
	})
}

func TestService_Create(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc, err := New(&mockkms.KeyManager{},
			&mockcrypto.Crypto{},
			mockstorage.NewMockStoreProvider(),
			createTestDocumentLoader(t),
			nil,
		)
		require.NoError(t, err)

		bytes, err := svc.Create("id", "k1")
		require.NoError(t, err)

		capability, err := zcapld.ParseCapability(bytes)
		require.NoError(t, err)
		require.Equal(t, capability.Context, zcapld.SecurityContextV2)
	})

	t.Run("failed to create signer for root capability", func(t *testing.T) {
		svc, err := New(&mockkms.KeyManager{CreateKeyErr: fmt.Errorf("failed to create key")},
			&mockcrypto.Crypto{},
			mockstorage.NewMockStoreProvider(),
			createTestDocumentLoader(t),
			nil,
		)
		require.NoError(t, err)

		_, err = svc.Create("id", "k1")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to create key")
	})

	t.Run("failed to store root capability in db", func(t *testing.T) {
		svc, err := New(&mockkms.KeyManager{},
			&mockcrypto.Crypto{},
			&mockstorage.MockStoreProvider{
				Store: &mockstorage.MockStore{
					Store:  make(map[string]mockstorage.DBEntry),
					ErrPut: fmt.Errorf("failed to store"),
				},
			},
			createTestDocumentLoader(t),
			nil,
		)
		require.NoError(t, err)

		_, err = svc.Create("id", "k1")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to store")
	})
}

func TestService_Handler(t *testing.T) {
	t.Run("test root capability not found", func(t *testing.T) {
		svc, err := New(&mockkms.KeyManager{},
			&mockcrypto.Crypto{},
			mockstorage.NewMockStoreProvider(),
			createTestDocumentLoader(t),
			nil,
		)
		require.NoError(t, err)

		h, err := svc.Handler("r1", &http.Request{Method: http.MethodGet}, nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get root capability r1 from db")
		require.Nil(t, h)
	})

	t.Run("test success", func(t *testing.T) {
		s := mockstorage.NewMockStoreProvider()

		bytes, err := json.Marshal(&zcapld.Capability{})
		require.NoError(t, err)

		require.NoError(t, s.Store.Put("r1", bytes))

		svc, err := New(&mockkms.KeyManager{},
			&mockcrypto.Crypto{},
			s,
			createTestDocumentLoader(t),
			nil,
		)
		require.NoError(t, err)

		h, err := svc.Handler("r1", &http.Request{Method: http.MethodGet}, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, h)
	})
}

func TestCapabilityResolver_Resolve(t *testing.T) {
	t.Run("test not found", func(t *testing.T) {
		svc, err := New(&mockkms.KeyManager{},
			&mockcrypto.Crypto{},
			mockstorage.NewMockStoreProvider(),
			createTestDocumentLoader(t),
			nil,
		)
		require.NoError(t, err)

		c := capabilityResolver{svc: svc}

		_, err = c.Resolve("r1")
		require.Error(t, err)
		require.Contains(t, err.Error(), "data not found")
	})

	t.Run("test success", func(t *testing.T) {
		s := mockstorage.NewMockStoreProvider()

		bytes, err := json.Marshal(&zcapld.Capability{})
		require.NoError(t, err)

		require.NoError(t, s.Store.Put("r1", bytes))

		svc, err := New(&mockkms.KeyManager{},
			&mockcrypto.Crypto{},
			s,
			createTestDocumentLoader(t),
			nil,
		)
		require.NoError(t, err)

		c := capabilityResolver{svc: svc}

		_, err = c.Resolve("r1")
		require.NoError(t, err)
	})
}

func TestLogError_Log(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		responseWriter := httptest.NewRecorder()

		l := logError{w: responseWriter}
		l.Log(fmt.Errorf("error"))

		require.Contains(t, responseWriter.Body.String(), "error")
	})
}

type mockProvider struct {
	ContextStore        ldstore.ContextStore
	RemoteProviderStore ldstore.RemoteProviderStore
}

func (m *mockProvider) JSONLDContextStore() ldstore.ContextStore {
	return m.ContextStore
}

func (m *mockProvider) JSONLDRemoteProviderStore() ldstore.RemoteProviderStore {
	return m.RemoteProviderStore
}

func createTestDocumentLoader(t *testing.T) *ld.DocumentLoader {
	t.Helper()

	p := &mockProvider{
		ContextStore:        mockldstore.NewMockContextStore(),
		RemoteProviderStore: mockldstore.NewMockRemoteProviderStore(),
	}

	loader, err := ld.NewDocumentLoader(p)
	require.NoError(t, err)

	return loader
}
