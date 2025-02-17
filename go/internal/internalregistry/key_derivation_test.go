// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
////////////////////////////////////////////////////////////////////////////////

package internalregistry_test

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"
	"github.com/google/tink/go/aead"
	"github.com/google/tink/go/core/registry"
	"github.com/google/tink/go/internal/internalregistry"
	"github.com/google/tink/go/internal/testing/stubkeymanager"
	"github.com/google/tink/go/subtle/random"
	gcmpb "github.com/google/tink/go/proto/aes_gcm_go_proto"
	tinkpb "github.com/google/tink/go/proto/tink_go_proto"
)

const (
	typeURLRoot           = "TestDeriveKeyFails"
	unregisteredKMTypeURL = typeURLRoot + "UnregisteredKeyManager"
	notDerivableKMTypeURL = typeURLRoot + "NotDerivableKeyManager"
	failingKMTypeURL      = typeURLRoot + "FailingKeyManager"
)

var once sync.Once

func mustRegisterBadKeyManagers(t *testing.T) {
	t.Helper()
	// The registry does not allow a key manager to be registered more than once.
	once.Do(func() {
		internalregistry.AllowKeyDerivation(unregisteredKMTypeURL)

		internalregistry.AllowKeyDerivation(notDerivableKMTypeURL)
		notDerivableKM := &stubkeymanager.StubKeyManager{URL: notDerivableKMTypeURL}
		if err := registry.RegisterKeyManager(notDerivableKM); err != nil {
			t.Fatalf("registry.RegisterKeyManager() err = %v, want nil", err)
		}

		internalregistry.AllowKeyDerivation(failingKMTypeURL)
		failingKM := &stubkeymanager.StubDerivableKeyManager{
			StubKeyManager: stubkeymanager.StubKeyManager{
				URL: failingKMTypeURL,
			},
			DerErr: errors.New("failing"),
		}
		if err := registry.RegisterKeyManager(failingKM); err != nil {
			t.Fatalf("registry.RegisterKeyManager() err = %v, want nil", err)
		}
	})
}

func TestDerivableKeyManagerFromKeyTemplate(t *testing.T) {
	for _, test := range []struct {
		name        string
		keyTemplate *tinkpb.KeyTemplate
	}{
		{"AES-128-GCM", aead.AES128GCMKeyTemplate()},
		{"AES-256-GCM", aead.AES256GCMKeyTemplate()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := internalregistry.DerivableKeyManagerFromKeyTemplate(test.keyTemplate); err != nil {
				t.Errorf("DerivableKeyManagerFromKeyTemplate() err = %v, want nil", err)
			}
		})
	}
}

func TestDerivableKeyManagerFromKeyTemplateRejectsInvalidInputs(t *testing.T) {
	mustRegisterBadKeyManagers(t)
	rand := random.GetRandomBytes(32)
	for _, test := range []struct {
		name        string
		keyTemplate *tinkpb.KeyTemplate
	}{
		{"nil key template", nil},
		{
			"derivation-disallowed but registered key manager",
			aead.AES128CTRHMACSHA256KeyTemplate(),
		},
		{
			"derivation-allowed but unregistered key manager",
			&tinkpb.KeyTemplate{
				TypeUrl:          unregisteredKMTypeURL,
				Value:            rand,
				OutputPrefixType: tinkpb.OutputPrefixType_TINK,
			},
		},
		{
			"does not implement DerivableKeyManager",
			&tinkpb.KeyTemplate{
				TypeUrl:          notDerivableKMTypeURL,
				Value:            rand,
				OutputPrefixType: tinkpb.OutputPrefixType_TINK,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := internalregistry.DerivableKeyManagerFromKeyTemplate(test.keyTemplate); err == nil {
				t.Error("DerivableKeyManagerFromKeyTemplate() err = nil, want non-nil")
			}
		})
	}
}

func TestDeriveKey(t *testing.T) {
	for _, test := range []struct {
		name            string
		keyTemplate     *tinkpb.KeyTemplate
		keySize         uint32
		keyMaterialType tinkpb.KeyData_KeyMaterialType
	}{
		{"AES-128-GCM", aead.AES128GCMKeyTemplate(), 16, tinkpb.KeyData_SYMMETRIC},
		{"AES-256-GCM", aead.AES256GCMKeyTemplate(), 32, tinkpb.KeyData_SYMMETRIC},
	} {
		t.Run(test.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			if _, err := buf.Write(random.GetRandomBytes(test.keySize)); err != nil {
				t.Fatalf("Write() err = %v, want nil", err)
			}
			keyData, err := internalregistry.DeriveKey(test.keyTemplate, buf)
			if err != nil {
				t.Fatalf("internalregistry.DeriveKey() err = %v, want nil", err)
			}
			if got, want := keyData.GetTypeUrl(), test.keyTemplate.GetTypeUrl(); got != want {
				t.Errorf("TypeUrl = %s, want %s", got, want)
			}
			key := &gcmpb.AesGcmKey{}
			if err := proto.Unmarshal(keyData.GetValue(), key); err != nil {
				t.Errorf("proto.Unmarshal() err = %v, want nil", err)
			}
			if got, want := len(key.GetKeyValue()), int(test.keySize); got != want {
				t.Errorf("len(KeyValue) = %d, want %d", got, want)
			}
			if got, want := keyData.GetKeyMaterialType(), test.keyMaterialType; got != want {
				t.Errorf("KeyMaterialType = %s, want %s", got, want)
			}
		})
	}
}

func TestDeriveKeyFails(t *testing.T) {
	mustRegisterBadKeyManagers(t)
	rand := random.GetRandomBytes(32)
	for _, test := range []struct {
		name        string
		keyTemplate *tinkpb.KeyTemplate
		randLen     uint32
	}{
		{"not enough randomness", aead.AES128GCMKeyTemplate(), 15},
		{"nil key template", nil, 32},
		{
			"derivation-disallowed but registered key manager",
			aead.AES128CTRHMACSHA256KeyTemplate(),
			32,
		},
		{
			"derivation-allowed but unregistered key manager",
			&tinkpb.KeyTemplate{
				TypeUrl:          unregisteredKMTypeURL,
				Value:            rand,
				OutputPrefixType: tinkpb.OutputPrefixType_TINK,
			},
			32,
		},
		{
			"does not implement DerivableKeyManager",
			&tinkpb.KeyTemplate{
				TypeUrl:          notDerivableKMTypeURL,
				Value:            rand,
				OutputPrefixType: tinkpb.OutputPrefixType_TINK,
			},
			32,
		},
		{
			"key manager with failing DeriveKey()",
			&tinkpb.KeyTemplate{
				TypeUrl:          failingKMTypeURL,
				Value:            rand,
				OutputPrefixType: tinkpb.OutputPrefixType_TINK,
			},
			32,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			if _, err := buf.Write(random.GetRandomBytes(test.randLen)); err != nil {
				t.Fatalf("Write() err = %v, want nil", err)
			}
			if _, err := internalregistry.DeriveKey(test.keyTemplate, buf); err == nil {
				t.Error("internalregistry.DeriveKey() err = nil, want non-nil")
			}
		})
	}
}
