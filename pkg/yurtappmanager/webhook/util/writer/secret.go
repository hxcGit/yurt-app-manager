/*
Copyright 2020 The OpenYurt Authors.
Copyright 2020 The Kruise Authors.
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package writer

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openyurtio/yurt-app-manager/pkg/yurtappmanager/webhook/util/generator"
)

// secretCertWriter provisions the certificate by reading and writing to the k8s secrets.
type secretCertWriter struct {
	*SecretCertWriterOptions

	// dnsName is the DNS name that the certificate is for.
	dnsName string
}

// SecretCertWriterOptions is options for constructing a secretCertWriter.
type SecretCertWriterOptions struct {
	// client talks to a kubernetes cluster for creating the secret.
	Client client.Client
	// certGenerator generates the certificates.
	CertGenerator generator.CertGenerator
	// secret points the secret that contains certificates that written by the CertWriter.
	Secret *types.NamespacedName
}

var _ CertWriter = &secretCertWriter{}

func (ops *SecretCertWriterOptions) setDefaults() {
	if ops.CertGenerator == nil {
		ops.CertGenerator = &generator.SelfSignedCertGenerator{}
	}
}

func (ops *SecretCertWriterOptions) validate() error {
	if ops.Client == nil {
		return errors.New("client must be set in SecretCertWriterOptions")
	}
	if ops.Secret == nil {
		return errors.New("secret must be set in SecretCertWriterOptions")
	}
	return nil
}

// NewSecretCertWriter constructs a CertWriter that persists the certificate in a k8s secret.
func NewSecretCertWriter(ops SecretCertWriterOptions) (CertWriter, error) {
	ops.setDefaults()
	err := ops.validate()
	if err != nil {
		return nil, err
	}
	return &secretCertWriter{
		SecretCertWriterOptions: &ops,
	}, nil
}

// EnsureCert provisions certificates for a webhookClientConfig by writing the certificates to a k8s secret.
func (s *secretCertWriter) EnsureCert(dnsName string) (*generator.Artifacts, bool, error) {
	// Create or refresh the certs based on clientConfig
	s.dnsName = dnsName
	return handleCommon(s.dnsName, s)
}

var _ certReadWriter = &secretCertWriter{}

func (s *secretCertWriter) buildSecret() (*corev1.Secret, *generator.Artifacts, error) {
	certs, err := s.CertGenerator.Generate(s.dnsName)
	if err != nil {
		return nil, nil, err
	}
	secret := certsToSecret(certs, *s.Secret)
	return secret, certs, err
}

func (s *secretCertWriter) write() (*generator.Artifacts, error) {
	secret, certs, err := s.buildSecret()
	if err != nil {
		return nil, err
	}
	err = s.Client.Create(context.TODO(), secret)
	if apierrors.IsAlreadyExists(err) {
		return nil, alreadyExistError{err}
	}
	return certs, err
}

func (s *secretCertWriter) overwrite(resourceVersion string) (
	*generator.Artifacts, error) {
	secret, certs, err := s.buildSecret()
	if err != nil {
		return nil, err
	}
	secret.ResourceVersion = resourceVersion
	err = s.Client.Update(context.TODO(), secret)
	klog.Infof("Cert writer update secret %s resourceVersion from %s to %s",
		secret.Name, resourceVersion, secret.ResourceVersion)
	return certs, err
}

func (s *secretCertWriter) read() (*generator.Artifacts, error) {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
	}
	err := s.Client.Get(context.TODO(), *s.Secret, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Errorf("Secret %v not found", s.Secret.String())
			return nil, notFoundError{err}
		}
		klog.Errorf("Get secret %v error %v", s.Secret.String(), err)
		return nil, err
	}
	certs := secretToCerts(secret)
	if certs != nil {
		// Store the CA for next usage.
		s.CertGenerator.SetCA(certs.CAKey, certs.CACert)
	}
	return certs, nil
}

func secretToCerts(secret *corev1.Secret) *generator.Artifacts {
	if secret.Data == nil {
		return &generator.Artifacts{ResourceVersion: secret.ResourceVersion}
	}
	return &generator.Artifacts{
		CAKey:           secret.Data[CAKeyName],
		CACert:          secret.Data[CACertName],
		Cert:            secret.Data[ServerCertName],
		Key:             secret.Data[ServerKeyName],
		ResourceVersion: secret.ResourceVersion,
	}
}

func certsToSecret(certs *generator.Artifacts, sec types.NamespacedName) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: sec.Namespace,
			Name:      sec.Name,
		},
		Data: map[string][]byte{
			CAKeyName:       certs.CAKey,
			CACertName:      certs.CACert,
			ServerKeyName:   certs.Key,
			ServerKeyName2:  certs.Key,
			ServerCertName:  certs.Cert,
			ServerCertName2: certs.Cert,
		},
	}
}
