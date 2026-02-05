package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	backupv1alpha1 "example.com/backup-operator/api/v1alpha1"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type s3Config struct {
	Endpoint        string
	Bucket          string
	Prefix          string
	Region          string
	ForcePathStyle  bool
	InsecureSkipTLS bool
	CABundle        []byte
	AccessKey       string
	SecretKey       string
	SessionToken    string
}

func storeArtifact(ctx context.Context, c client.Client, storage *backupv1alpha1.BackupStorageLocation, artifactPath string, backup *backupObject, timestamp string) (string, error) {
	relativeBase := path.Join(clusterID(), strings.ToLower(backup.kind), namespaceSegment(backup.namespace), backup.name, timestamp)

	switch storage.Spec.Type {
	case backupv1alpha1.StorageLocationS3:
		cfg, err := loadS3Config(ctx, c, storage)
		if err != nil {
			return "", err
		}
		key := path.Join(cfg.Prefix, relativeBase, artifactFileName)
		if err := uploadToS3(ctx, cfg, key, artifactPath); err != nil {
			return "", err
		}
		return fmt.Sprintf("s3://%s/%s", cfg.Bucket, key), nil
	case backupv1alpha1.StorageLocationNFS:
		nfsPath, err := storeToNFS(storage, relativeBase, artifactPath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("nfs://%s%s", storage.Spec.NFS.Server, nfsPath), nil
	default:
		return "", fmt.Errorf("unsupported storage type %q", storage.Spec.Type)
	}
}

func loadArtifact(ctx context.Context, c client.Client, storage *backupv1alpha1.BackupStorageLocation, artifactLocation, destPath string) error {
	switch storage.Spec.Type {
	case backupv1alpha1.StorageLocationS3:
		cfg, err := loadS3Config(ctx, c, storage)
		if err != nil {
			return err
		}
		bucket, key, err := parseS3Location(artifactLocation)
		if err != nil {
			return err
		}
		cfg.Bucket = bucket
		return downloadFromS3(ctx, cfg, key, destPath)
	case backupv1alpha1.StorageLocationNFS:
		return loadFromNFS(storage, artifactLocation, destPath)
	default:
		return fmt.Errorf("unsupported storage type %q", storage.Spec.Type)
	}
}

func namespaceSegment(ns string) string {
	if ns == "" {
		return "cluster"
	}
	return ns
}

func loadS3Config(ctx context.Context, c client.Client, storage *backupv1alpha1.BackupStorageLocation) (*s3Config, error) {
	if storage.Spec.S3 == nil {
		return nil, fmt.Errorf("s3 configuration is missing")
	}

	cfg := &s3Config{
		Endpoint:        storage.Spec.S3.Endpoint,
		Bucket:          storage.Spec.S3.Bucket,
		Prefix:          strings.Trim(storage.Spec.S3.Prefix, "/"),
		Region:          storage.Spec.S3.Region,
		ForcePathStyle:  storage.Spec.S3.ForcePathStyle,
		InsecureSkipTLS: storage.Spec.S3.InsecureSkipTLS,
		CABundle:        storage.Spec.S3.CABundle,
	}

	secretRef := storage.Spec.S3.SecretRef
	if secretRef.Name != "" {
		secret := &corev1.Secret{}
		key := client.ObjectKey{Name: secretRef.Name, Namespace: secretRef.Namespace}
		if key.Namespace == "" {
			key.Namespace = operatorNamespace()
		}
		if err := c.Get(ctx, key, secret); err != nil {
			return nil, err
		}
		cfg.AccessKey = string(secret.Data["accessKey"])
		cfg.SecretKey = string(secret.Data["secretKey"])
		cfg.SessionToken = string(secret.Data["sessionToken"])
	}

	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	return cfg, nil
}

func uploadToS3(ctx context.Context, cfg *s3Config, key, artifactPath string) error {
	client, err := buildS3Client(ctx, cfg)
	if err != nil {
		return err
	}

	file, err := os.Open(artifactPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &cfg.Bucket,
		Key:    &key,
		Body:   file,
	})
	return err
}

func downloadFromS3(ctx context.Context, cfg *s3Config, key, destPath string) error {
	client, err := buildS3Client(ctx, cfg)
	if err != nil {
		return err
	}

	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &cfg.Bucket,
		Key:    &key,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func buildS3Client(ctx context.Context, cfg *s3Config) (*s3.Client, error) {
	customTransport := &http.Transport{}
	if cfg.InsecureSkipTLS || len(cfg.CABundle) > 0 {
		tlsConfig := &tls.Config{InsecureSkipVerify: cfg.InsecureSkipTLS}
		if len(cfg.CABundle) > 0 {
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(cfg.CABundle)
			tlsConfig.RootCAs = pool
		}
		customTransport.TLSClientConfig = tlsConfig
	}

	customHTTP := &http.Client{Transport: customTransport}

	resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...any) (aws.Endpoint, error) {
		if service == s3.ServiceID && cfg.Endpoint != "" {
			return aws.Endpoint{URL: cfg.Endpoint, SigningRegion: cfg.Region, HostnameImmutable: true}, nil
		}
		return aws.Endpoint{}, &aws.EndpointNotFoundError{}
	})

	loadOptions := []func(*config.LoadOptions) error{
		config.WithRegion(cfg.Region),
		config.WithEndpointResolverWithOptions(resolver),
		config.WithHTTPClient(customHTTP),
	}
	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		credProvider := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, cfg.SessionToken)
		loadOptions = append(loadOptions, config.WithCredentialsProvider(credProvider))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, err
	}

	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.ForcePathStyle
	}), nil
}

func storeToNFS(storage *backupv1alpha1.BackupStorageLocation, relativeBase, artifactPath string) (string, error) {
	if storage.Spec.NFS == nil {
		return "", fmt.Errorf("nfs configuration is missing")
	}
	mountPath := getEnvOrDefault("NFS_MOUNT_PATH", "/data")
	destDir := filepath.Join(mountPath, filepath.FromSlash(relativeBase))
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return "", err
	}
	destPath := filepath.Join(destDir, artifactFileName)
	if err := copyFile(artifactPath, destPath); err != nil {
		return "", err
	}
	return path.Join(storage.Spec.NFS.Path, relativeBase, artifactFileName), nil
}

func loadFromNFS(storage *backupv1alpha1.BackupStorageLocation, artifactLocation, destPath string) error {
	if storage.Spec.NFS == nil {
		return fmt.Errorf("nfs configuration is missing")
	}
	mountPath := getEnvOrDefault("NFS_MOUNT_PATH", "/data")

	trimmed := strings.TrimPrefix(artifactLocation, "nfs://")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) < 2 {
		return fmt.Errorf("invalid nfs artifact location")
	}
	fullPath := "/" + parts[1]
	basePath := strings.TrimRight(storage.Spec.NFS.Path, "/")
	relPath := strings.TrimPrefix(fullPath, basePath)
	relPath = strings.TrimPrefix(relPath, "/")

	sourcePath := filepath.Join(mountPath, filepath.FromSlash(relPath))
	return copyFile(sourcePath, destPath)
}

func parseS3Location(location string) (string, string, error) {
	trimmed := strings.TrimPrefix(location, "s3://")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid s3 location %q", location)
	}
	return parts[0], parts[1], nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
