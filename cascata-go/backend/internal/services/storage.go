package services

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	internalTypes "cascata-backend/internal/types"
)

// StorageProviderType represents all supported storage providers
type StorageProviderType string

const (
	ProviderLocal            StorageProviderType = "local"
	ProviderS3               StorageProviderType = "s3"
	ProviderCloudinary       StorageProviderType = "cloudinary"
	ProviderImageKit         StorageProviderType = "imagekit"
	ProviderCloudflareImages StorageProviderType = "cloudflare_images"
	ProviderGDrive           StorageProviderType = "gdrive"
	ProviderDropbox          StorageProviderType = "dropbox"
	ProviderOneDrive         StorageProviderType = "onedrive"
)

// S3Config configuration for S3-compatible storage
type S3Config struct {
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	Endpoint        string `json:"endpoint"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	PublicURLBase   string `json:"publicUrlBase"`
}

// CloudinaryConfig configuration
type CloudinaryConfig struct {
	CloudName    string `json:"cloudName"`
	APIKey       string `json:"apiKey"`
	APISecret    string `json:"apiSecret"`
	UploadPreset string `json:"uploadPreset"`
}

// ImageKitConfig configuration
type ImageKitConfig struct {
	PublicKey   string `json:"publicKey"`
	PrivateKey  string `json:"privateKey"`
	URLEndpoint string `json:"urlEndpoint"`
}

// CloudflareConfig configuration
type CloudflareConfig struct {
	AccountID string `json:"accountId"`
	APIToken  string `json:"apiToken"`
	Variant   string `json:"variant"`
}

// GDriveConfig configuration
type GDriveConfig struct {
	ClientEmail  string `json:"clientEmail"`
	PrivateKey   string `json:"privateKey"`
	RootFolderID string `json:"rootFolderId"`
}

// DropboxConfig configuration
type DropboxConfig struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
}

// OneDriveConfig configuration
type OneDriveConfig struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
}

// ProjectStorageConfig holds the complete storage configuration for a project
type ProjectStorageConfig struct {
	Provider   StorageProviderType `json:"provider"`
	Optimize   bool                `json:"optimize"`
	S3         *S3Config           `json:"s3,omitempty"`
	Cloudinary *CloudinaryConfig   `json:"cloudinary,omitempty"`
	ImageKit   *ImageKitConfig     `json:"imagekit,omitempty"`
	Cloudflare *CloudflareConfig   `json:"cloudflare,omitempty"`
	GDrive     *GDriveConfig       `json:"gdrive,omitempty"`
	Dropbox    *DropboxConfig      `json:"dropbox,omitempty"`
	OneDrive   *OneDriveConfig     `json:"onedrive,omitempty"`
}

// GovernanceRule defines storage governance for a sector
type GovernanceRule struct {
	MaxSize         string   `json:"max_size"`
	MaxSizeDirect   string   `json:"max_size_direct"`
	AllowedExts     []string `json:"allowed_exts"`
	StorageProvider string   `json:"storage_provider,omitempty"`
}

// GovernanceConfig holds all sector rules
type GovernanceConfig map[string]GovernanceRule

// SectorDefinition defines a file sector
type SectorDefinition struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Desc     string   `json:"desc"`
	Exts     []string `json:"exts"`
	Defaults []string `json:"defaults"`
}

// UploadResult holds the result of an upload operation
type UploadResult struct {
	URL      string            `json:"url"`
	Method   string            `json:"method"`
	Headers  map[string]string `json:"headers,omitempty"`
	Strategy string            `json:"strategy"` // "direct" or "proxy"
}

// StorageService handles multi-provider storage operations
type StorageService struct {
	httpClient *http.Client
}

// NewStorageService creates a new storage service
func NewStorageService() *StorageService {
	return &StorageService{
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// GetPhysicalDiskUsage calculates disk usage using 'du' command (fastest method)
func (s *StorageService) GetPhysicalDiskUsage(projectSlug, root string) (int64, error) {
	targetDir := filepath.Join(root, projectSlug)

	// Check if directory exists
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		return 0, nil
	}

	// Use 'du -sb' for fast byte calculation
	cmd := exec.Command("du", "-sb", targetDir)
	output, err := cmd.Output()
	if err != nil {
		// Fallback to manual walk if du fails
		return s.fallbackDiskUsage(targetDir)
	}

	// Parse output: "12345 /path/to/dir"
	parts := strings.Fields(string(output))
	if len(parts) == 0 {
		return 0, fmt.Errorf("invalid du output")
	}

	bytes, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}

	return bytes, nil
}

// fallbackDiskUsage manually walks directory if du fails
func (s *StorageService) fallbackDiskUsage(targetDir string) (int64, error) {
	var size int64
	err := filepath.Walk(targetDir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// CreateUploadUrl generates a presigned upload URL for direct uploads
func (s *StorageService) CreateUploadUrl(key, contentType string, config *ProjectStorageConfig) (*UploadResult, error) {
	switch config.Provider {
	case ProviderS3:
		if config.S3 == nil {
			return nil, fmt.Errorf("S3 config missing")
		}
		return s.createS3PresignedURL(key, contentType, config.S3)
	default:
		// For other providers, return proxy strategy
		return &UploadResult{
			Strategy: "proxy",
			Method:   "POST",
			Headers:  map[string]string{"Content-Type": "multipart/form-data"},
		}, nil
	}
}

// createS3PresignedURL creates a presigned URL for S3 upload using AWS SDK V4 signing
func (s *StorageService) createS3PresignedURL(key, contentType string, s3Config *S3Config) (*UploadResult, error) {
	// Configure AWS SDK credentials
	creds := credentials.NewStaticCredentialsProvider(
		s3Config.AccessKeyID,
		s3Config.SecretAccessKey,
		"",
	)

	// Configure AWS SDK options
	optFns := []func(*awsConfig.LoadOptions) error{
		awsConfig.WithCredentialsProvider(creds),
		awsConfig.WithRegion(s3Config.Region),
	}

	// Use custom endpoint if provided (for MinIO, etc.)
	if s3Config.Endpoint != "" {
		optFns = append(optFns, awsConfig.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					URL:               s3Config.Endpoint,
					HostnameImmutable: true,
					Source:            aws.EndpointSourceCustom,
				}, nil
			}),
		))
	}

	// Load config
	cfg, err := awsConfig.LoadDefaultConfig(context.Background(), optFns...)
	if err != nil {
		// Fallback to proxy strategy on config error
		return &UploadResult{
			Strategy: "proxy",
			Method:   "POST",
			Headers:  map[string]string{"Content-Type": "multipart/form-data"},
		}, nil
	}

	// Create S3 client
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if s3Config.Endpoint != "" {
			o.BaseEndpoint = aws.String(s3Config.Endpoint)
		}
	})

	// Create presigner
	presigner := s3.NewPresignClient(client)

	// Generate presigned URL
	putInput := &s3.PutObjectInput{
		Bucket:      aws.String(s3Config.Bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}

	// Add ACL if public read
	putInput.ACL = types.ObjectCannedACLPublicRead

	presignedReq, err := presigner.PresignPutObject(context.Background(), putInput,
		s3.WithPresignExpires(15*time.Minute),
	)
	if err != nil {
		// Fallback to proxy strategy
		return &UploadResult{
			Strategy: "proxy",
			Method:   "POST",
			Headers:  map[string]string{"Content-Type": "multipart/form-data"},
		}, nil
	}

	return &UploadResult{
		Strategy: "direct",
		URL:      presignedReq.URL,
		Method:   "PUT",
		Headers: map[string]string{
			"Content-Type": contentType,
		},
	}, nil
}

// Upload handles file upload to various providers
func (s *StorageService) Upload(ctx context.Context, file multipart.File, header *multipart.FileHeader, projectSlug, bucketName, targetPath string, config *ProjectStorageConfig) (string, error) {
	fullKey := filepath.Join(targetPath, header.Filename)
	fullKey = strings.ReplaceAll(fullKey, "\\", "/")
	fullKey = strings.TrimLeft(fullKey, "/")

	switch config.Provider {
	case ProviderLocal:
		return "", nil // Local is handled by controller

	case ProviderS3:
		if config.S3 == nil {
			return "", fmt.Errorf("S3 config missing")
		}
		return s.uploadS3(ctx, file, header, fullKey, config.S3)

	case ProviderCloudinary:
		if config.Cloudinary == nil {
			return "", fmt.Errorf("Cloudinary config missing")
		}
		return s.uploadCloudinary(ctx, file, header, targetPath, config.Cloudinary)

	case ProviderImageKit:
		if config.ImageKit == nil {
			return "", fmt.Errorf("ImageKit config missing")
		}
		return s.uploadImageKit(ctx, file, header, fullKey, config.ImageKit)

	case ProviderCloudflareImages:
		if config.Cloudflare == nil {
			return "", fmt.Errorf("Cloudflare config missing")
		}
		return s.uploadCloudflare(ctx, file, header, config.Cloudflare)

	case ProviderGDrive:
		if config.GDrive == nil {
			return "", fmt.Errorf("GDrive config missing")
		}
		return s.uploadGDrive(ctx, file, header, targetPath, config.GDrive)

	case ProviderDropbox:
		if config.Dropbox == nil {
			return "", fmt.Errorf("Dropbox config missing")
		}
		return s.uploadDropbox(ctx, file, header, fullKey, config.Dropbox)

	case ProviderOneDrive:
		if config.OneDrive == nil {
			return "", fmt.Errorf("OneDrive config missing")
		}
		return s.uploadOneDrive(ctx, file, header, fullKey, config.OneDrive)

	default:
		return "", fmt.Errorf("unsupported provider: %s", config.Provider)
	}
}

// uploadS3 uploads to S3-compatible storage using streaming (no memory buffer)
func (s *StorageService) uploadS3(ctx context.Context, file multipart.File, header *multipart.FileHeader, key string, s3Config *S3Config) (string, error) {
	fmt.Printf("[S3 Upload] Starting upload to bucket '%s', key '%s', endpoint '%s'\n", 
		s3Config.Bucket, key, s3Config.Endpoint)
	
	// Configure AWS SDK credentials
	creds := credentials.NewStaticCredentialsProvider(
		s3Config.AccessKeyID,
		s3Config.SecretAccessKey,
		"",
	)
	fmt.Printf("[S3 Upload] Using AccessKeyID: %s..., Region: %s\n", 
		s3Config.AccessKeyID[:min(8, len(s3Config.AccessKeyID))], s3Config.Region)

	optFns := []func(*awsConfig.LoadOptions) error{
		awsConfig.WithCredentialsProvider(creds),
		awsConfig.WithRegion(s3Config.Region),
	}

	if s3Config.Endpoint != "" {
		fmt.Printf("[S3 Upload] Using custom endpoint: %s\n", s3Config.Endpoint)
		optFns = append(optFns, awsConfig.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					URL:               s3Config.Endpoint,
					HostnameImmutable: true,
					Source:            aws.EndpointSourceCustom,
				}, nil
			}),
		))
	}

	cfg, err := awsConfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		fmt.Printf("[S3 Upload] ERROR: Failed to load AWS config: %v\n", err)
		return "", fmt.Errorf("failed to load AWS config: %w", err)
	}
	fmt.Printf("[S3 Upload] AWS config loaded successfully\n")

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if s3Config.Endpoint != "" {
			o.BaseEndpoint = aws.String(s3Config.Endpoint)
		}
	})
	fmt.Printf("[S3 Upload] S3 client created\n")

	// Create uploader with streaming (5MB part size for multipart)
	uploader := manager.NewUploader(client, func(u *manager.Uploader) {
		u.PartSize = 5 * 1024 * 1024 // 5MB parts
		u.Concurrency = 5
	})
	fmt.Printf("[S3 Upload] Uploader created (5MB parts, concurrency=5)\n")

	// Upload using streaming (no full buffer in memory)
	fmt.Printf("[S3 Upload] Starting upload...\n")
	_, err = uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s3Config.Bucket),
		Key:         aws.String(key),
		Body:        file,
		ContentType: aws.String(header.Header.Get("Content-Type")),
		ACL:         types.ObjectCannedACLPublicRead,
	})
	if err != nil {
		fmt.Printf("[S3 Upload] ERROR: Upload failed: %v\n", err)
		return "", fmt.Errorf("S3 upload failed: %w", err)
	}
	fmt.Printf("[S3 Upload] Upload successful!\n")

	// Return public URL
	if s3Config.PublicURLBase != "" {
		return fmt.Sprintf("%s/%s", strings.TrimRight(s3Config.PublicURLBase, "/"), key), nil
	}
	if s3Config.Endpoint != "" {
		return fmt.Sprintf("%s/%s/%s", strings.TrimRight(s3Config.Endpoint, "/"), s3Config.Bucket, key), nil
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s3Config.Bucket, s3Config.Region, key), nil
}

// uploadCloudinary uploads to Cloudinary
func (s *StorageService) uploadCloudinary(ctx context.Context, file multipart.File, header *multipart.FileHeader, folder string, config *CloudinaryConfig) (string, error) {
	if config.UploadPreset == "" {
		return "", fmt.Errorf("Cloudinary upload preset required")
	}

	// Build multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add fields
	writer.WriteField("api_key", config.APIKey)
	writer.WriteField("timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	if folder != "" {
		writer.WriteField("folder", folder)
	}
	writer.WriteField("upload_preset", config.UploadPreset)

	// Add file
	part, err := writer.CreateFormFile("file", header.Filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	writer.Close()

	// Create request
	url := fmt.Sprintf("https://api.cloudinary.com/v1_1/%s/auto/upload", config.CloudName)
	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Cloudinary upload failed: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		SecureURL string `json:"secure_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.SecureURL, nil
}

// uploadImageKit uploads to ImageKit
func (s *StorageService) uploadImageKit(ctx context.Context, file multipart.File, header *multipart.FileHeader, key string, config *ImageKitConfig) (string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add fields
	writer.WriteField("fileName", header.Filename)
	writer.WriteField("useUniqueFileName", "false")

	folder := filepath.Dir(key)
	if folder != "" && folder != "." {
		writer.WriteField("folder", folder)
	}

	// Add file
	part, err := writer.CreateFormFile("file", header.Filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	writer.Close()

	// Create request with auth
	auth := base64.StdEncoding.EncodeToString([]byte(config.PrivateKey + ":"))
	url := "https://upload.imagekit.io/api/v1/files/upload"

	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Basic "+auth)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ImageKit upload failed: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.URL, nil
}

// uploadCloudflare uploads to Cloudflare Images
func (s *StorageService) uploadCloudflare(ctx context.Context, file multipart.File, header *multipart.FileHeader, config *CloudflareConfig) (string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add file
	part, err := writer.CreateFormFile("file", header.Filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	writer.Close()

	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/images/v1", config.AccountID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+config.APIToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Cloudflare upload failed: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		Result struct {
			Variants []string `json:"variants"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Result.Variants) > 0 {
		return result.Result.Variants[0], nil
	}

	return "", fmt.Errorf("no variants returned")
}

// uploadGDrive uploads to Google Drive
func (s *StorageService) uploadGDrive(ctx context.Context, file multipart.File, header *multipart.FileHeader, targetPath string, config *GDriveConfig) (string, error) {
	// Get access token
	token, err := s.getGDriveToken(config)
	if err != nil {
		return "", fmt.Errorf("failed to get GDrive token: %w", err)
	}

	// Build multipart upload (metadata + file)
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Metadata part
	metadata := map[string]interface{}{
		"name": header.Filename,
	}
	if config.RootFolderID != "" {
		metadata["parents"] = []string{config.RootFolderID}
	}
	metadataJSON, _ := json.Marshal(metadata)

	metadataPart, _ := writer.CreatePart(map[string][]string{
		"Content-Type":        {"application/json; charset=UTF-8"},
		"Content-Disposition": {`form-data; name="metadata"`},
	})
	metadataPart.Write(metadataJSON)

	// File part
	filePart, _ := writer.CreatePart(map[string][]string{
		"Content-Type":        {header.Header.Get("Content-Type")},
		"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename="%s"`, header.Filename)},
	})
	if _, err := io.Copy(filePart, file); err != nil {
		return "", err
	}

	writer.Close()

	url := "https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id,webViewLink"
	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GDrive upload failed: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		WebViewLink string `json:"webViewLink"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.WebViewLink, nil
}

// getGDriveToken generates a JWT token for Google Drive using RS256 signing
func (s *StorageService) getGDriveToken(config *GDriveConfig) (string, error) {
	now := time.Now()

	// Create JWT claims
	claims := jwt.MapClaims{
		"iss":   config.ClientEmail,
		"scope": "https://www.googleapis.com/auth/drive",
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}

	// Create token with RS256 signing method
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	// Parse the private key from PEM format
	privateKeyPEM := config.PrivateKey
	if privateKeyPEM == "" {
		return "", fmt.Errorf("GDrive private key is empty")
	}

	// Decode PEM
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", fmt.Errorf("failed to decode GDrive private key PEM")
	}

	// Parse PKCS8 private key
	privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1 format
		privateKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("failed to parse GDrive private key: %w", err)
		}
	}

	// Sign the token
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign GDrive JWT: %w", err)
	}

	// Exchange JWT for access token
	data := url.Values{}
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	data.Set("assertion", signedToken)

	resp, err := s.httpClient.PostForm("https://oauth2.googleapis.com/token", data)
	if err != nil {
		return "", fmt.Errorf("failed to exchange GDrive JWT: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode GDrive token response: %w", err)
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("GDrive OAuth error: %s - %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	return tokenResp.AccessToken, nil
}

// uploadDropbox uploads to Dropbox using streaming (no memory buffer)
func (s *StorageService) uploadDropbox(ctx context.Context, file multipart.File, header *multipart.FileHeader, key string, config *DropboxConfig) (string, error) {
	// Get access token
	token, err := s.getDropboxToken(config)
	if err != nil {
		return "", fmt.Errorf("failed to get Dropbox token: %w", err)
	}

	dropboxPath := "/" + key

	// Use streaming upload - Dropbox supports direct streaming
	uploadURL := "https://content.dropboxapi.com/2/files/upload"
	
	// Create API args header
	apiArgs := fmt.Sprintf(`{"path":"%s","mode":"add","autorename":true,"mute":false}`, dropboxPath)
	
	req, err := http.NewRequestWithContext(ctx, "POST", uploadURL, file)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Dropbox-API-Arg", apiArgs)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = header.Size

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Dropbox upload failed: %d - %s", resp.StatusCode, string(body))
	}

	var uploadResult struct {
		PathDisplay string `json:"path_display"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&uploadResult); err != nil {
		return "", err
	}

	// Create shared link
	shareURL := "https://api.dropboxapi.com/2/sharing/create_shared_link_with_settings"
	shareBody := fmt.Sprintf(`{"path":"%s"}`, uploadResult.PathDisplay)
	shareReq, err := http.NewRequestWithContext(ctx, "POST", shareURL, strings.NewReader(shareBody))
	if err != nil {
		return fmt.Sprintf("https://www.dropbox.com/home%s", dropboxPath), nil
	}
	shareReq.Header.Set("Authorization", "Bearer "+token)
	shareReq.Header.Set("Content-Type", "application/json")

	shareResp, err := s.httpClient.Do(shareReq)
	if err != nil {
		return fmt.Sprintf("https://www.dropbox.com/home%s", dropboxPath), nil
	}
	defer shareResp.Body.Close()

	if shareResp.StatusCode == http.StatusOK {
		var shareResult struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(shareResp.Body).Decode(&shareResult); err == nil {
			return strings.Replace(shareResult.URL, "?dl=0", "?dl=1", 1), nil
		}
	}

	return fmt.Sprintf("https://www.dropbox.com/home%s", dropboxPath), nil
}

// getDropboxToken gets a Dropbox access token
func (s *StorageService) getDropboxToken(config *DropboxConfig) (string, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", config.RefreshToken)
	data.Set("client_id", config.ClientID)
	data.Set("client_secret", config.ClientSecret)

	resp, err := s.httpClient.PostForm("https://api.dropbox.com/oauth2/token", data)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("Dropbox token error: %s", tokenResp.Error)
	}

	return tokenResp.AccessToken, nil
}

// uploadOneDrive uploads to OneDrive using streaming
func (s *StorageService) uploadOneDrive(ctx context.Context, file multipart.File, header *multipart.FileHeader, key string, config *OneDriveConfig) (string, error) {
	// Get access token
	token, err := s.getOneDriveToken(config)
	if err != nil {
		return "", fmt.Errorf("failed to get OneDrive token: %w", err)
	}

	// Upload using streaming
	uploadURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/root:/%s:/content", key)

	req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, file)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", header.Header.Get("Content-Type"))
	req.ContentLength = header.Size

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OneDrive upload failed: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		DownloadURL string `json:"@microsoft.graph.downloadUrl"`
		WebURL      string `json:"webUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.DownloadURL != "" {
		return result.DownloadURL, nil
	}
	return result.WebURL, nil
}

// getOneDriveToken gets a OneDrive access token
func (s *StorageService) getOneDriveToken(config *OneDriveConfig) (string, error) {
	data := url.Values{}
	data.Set("client_id", config.ClientID)
	data.Set("client_secret", config.ClientSecret)
	data.Set("refresh_token", config.RefreshToken)
	data.Set("grant_type", "refresh_token")
	data.Set("scope", "Files.ReadWrite.All")

	resp, err := s.httpClient.PostForm("https://login.microsoftonline.com/common/oauth2/v2.0/token", data)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("OneDrive token error: %s - %s", tokenResp.Error, tokenResp.Description)
	}

	return tokenResp.AccessToken, nil
}

// Delete removes a file from the storage provider
func (s *StorageService) Delete(ctx context.Context, key string, config *ProjectStorageConfig) error {
	cleanKey := strings.TrimLeft(key, "/")

	switch config.Provider {
	case ProviderS3:
		if config.S3 == nil {
			return fmt.Errorf("S3 config missing")
		}
		return s.deleteS3(ctx, cleanKey, config.S3)

	case ProviderCloudinary:
		if config.Cloudinary == nil {
			return fmt.Errorf("Cloudinary config missing")
		}
		return s.deleteCloudinary(ctx, cleanKey, config.Cloudinary)

	case ProviderImageKit:
		if config.ImageKit == nil {
			return fmt.Errorf("ImageKit config missing")
		}
		return s.deleteImageKit(ctx, cleanKey, config.ImageKit)

	case ProviderDropbox:
		if config.Dropbox == nil {
			return fmt.Errorf("Dropbox config missing")
		}
		return s.deleteDropbox(ctx, "/"+cleanKey, config.Dropbox)

	case ProviderOneDrive:
		if config.OneDrive == nil {
			return fmt.Errorf("OneDrive config missing")
		}
		return s.deleteOneDrive(ctx, cleanKey, config.OneDrive)

	case ProviderGDrive:
		if config.GDrive == nil {
			return fmt.Errorf("GDrive config missing")
		}
		return s.deleteGDrive(ctx, cleanKey, config.GDrive)

	default:
		return fmt.Errorf("delete not supported for provider: %s", config.Provider)
	}
}

// deleteS3 deletes from S3 using AWS SDK with proper signing
func (s *StorageService) deleteS3(ctx context.Context, key string, s3Config *S3Config) error {
	// Configure AWS SDK credentials
	creds := credentials.NewStaticCredentialsProvider(
		s3Config.AccessKeyID,
		s3Config.SecretAccessKey,
		"",
	)

	optFns := []func(*awsConfig.LoadOptions) error{
		awsConfig.WithCredentialsProvider(creds),
		awsConfig.WithRegion(s3Config.Region),
	}

	if s3Config.Endpoint != "" {
		optFns = append(optFns, awsConfig.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					URL:               s3Config.Endpoint,
					HostnameImmutable: true,
					Source:            aws.EndpointSourceCustom,
				}, nil
			}),
		))
	}

	cfg, err := awsConfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if s3Config.Endpoint != "" {
			o.BaseEndpoint = aws.String(s3Config.Endpoint)
		}
	})

	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s3Config.Bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return fmt.Errorf("S3 delete failed: %w", err)
	}

	return nil
}

// deleteCloudinary deletes from Cloudinary
func (s *StorageService) deleteCloudinary(ctx context.Context, key string, config *CloudinaryConfig) error {
	publicID := strings.TrimSuffix(key, filepath.Ext(key))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Create signature
	sigData := fmt.Sprintf("public_id=%s&timestamp=%s%s", publicID, timestamp, config.APISecret)
	h := sha1.New()
	h.Write([]byte(sigData))
	signature := fmt.Sprintf("%x", h.Sum(nil))

	data := url.Values{}
	data.Set("public_id", publicID)
	data.Set("api_key", config.APIKey)
	data.Set("timestamp", timestamp)
	data.Set("signature", signature)

	url := fmt.Sprintf("https://api.cloudinary.com/v1_1/%s/image/destroy", config.CloudName)
	resp, err := s.httpClient.PostForm(url, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Cloudinary delete failed: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

// deleteImageKit deletes from ImageKit
func (s *StorageService) deleteImageKit(ctx context.Context, filePath string, config *ImageKitConfig) error {
	auth := base64.StdEncoding.EncodeToString([]byte(config.PrivateKey + ":"))

	// Search for file
	searchURL := fmt.Sprintf("https://api.imagekit.io/v1/files?searchQuery=name=\"%s\"&limit=1", filepath.Base(filePath))
	searchReq, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return err
	}
	searchReq.Header.Set("Authorization", "Basic "+auth)

	searchResp, err := s.httpClient.Do(searchReq)
	if err != nil {
		return err
	}
	defer searchResp.Body.Close()

	var searchResult []struct {
		FileID string `json:"fileId"`
	}
	if err := json.NewDecoder(searchResp.Body).Decode(&searchResult); err != nil {
		return err
	}

	if len(searchResult) == 0 {
		return fmt.Errorf("file not found in ImageKit")
	}

	// Delete file
	deleteURL := fmt.Sprintf("https://api.imagekit.io/v1/files/%s", searchResult[0].FileID)
	deleteReq, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return err
	}
	deleteReq.Header.Set("Authorization", "Basic "+auth)

	deleteResp, err := s.httpClient.Do(deleteReq)
	if err != nil {
		return err
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusNoContent && deleteResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(deleteResp.Body)
		return fmt.Errorf("ImageKit delete failed: %d - %s", deleteResp.StatusCode, string(body))
	}

	return nil
}

// deleteDropbox deletes from Dropbox
func (s *StorageService) deleteDropbox(ctx context.Context, dropboxPath string, config *DropboxConfig) error {
	token, err := s.getDropboxToken(config)
	if err != nil {
		return err
	}

	body := fmt.Sprintf(`{"path":"%s"}`, dropboxPath)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.dropboxapi.com/2/files/delete_v2", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Dropbox delete failed: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

// deleteOneDrive deletes from OneDrive
func (s *StorageService) deleteOneDrive(ctx context.Context, key string, config *OneDriveConfig) error {
	token, err := s.getOneDriveToken(config)
	if err != nil {
		return err
	}

	deleteURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/root:/%s", key)
	req, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OneDrive delete failed: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

// deleteGDrive deletes from Google Drive
func (s *StorageService) deleteGDrive(ctx context.Context, key string, config *GDriveConfig) error {
	token, err := s.getGDriveToken(config)
	if err != nil {
		return err
	}

	fileName := filepath.Base(key)

	// Search by name and ensure parent folder matches if configured
	q := fmt.Sprintf("name = '%s' and trashed = false", fileName)
	if config.RootFolderID != "" {
		q += fmt.Sprintf(" and '%s' in parents", config.RootFolderID)
	}

	searchURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files?q=%s&fields=files(id)", url.QueryEscape(q))
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GDrive search failed: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	// Delete all matching files
	for _, file := range result.Files {
		deleteURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s", file.ID)
		deleteReq, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
		if err != nil {
			continue
		}
		deleteReq.Header.Set("Authorization", "Bearer "+token)

		deleteResp, err := s.httpClient.Do(deleteReq)
		if err != nil {
			continue
		}
		deleteResp.Body.Close()
	}

	return nil
}

// GetSectorForExt returns the sector for a file extension
func GetSectorForExt(ext string) string {
	ext = strings.ToLower(strings.TrimLeft(ext, "."))

	sectorMap := map[string][]string{
		"visual":     {"jpg", "jpeg", "png", "gif", "webp", "svg", "bmp", "ico", "tiff", "avif", "heic", "heif"},
		"motion":     {"mp4", "mov", "avi", "mkv", "webm", "flv", "wmv", "m4v", "mpg", "mpeg", "3gp"},
		"audio":      {"mp3", "wav", "flac", "aac", "ogg", "m4a", "wma", "m4p", "amr", "mid", "midi", "opus"},
		"docs":       {"pdf", "doc", "docx", "odt", "rtf", "txt", "pages", "epub", "mobi", "azw3"},
		"structured": {"csv", "json", "xml", "yaml", "yml", "sql", "xls", "xlsx", "ods", "tsv", "parquet", "avro"},
		"archives":   {"zip", "rar", "7z", "tar", "gz", "bz2", "iso", "dmg", "pkg", "xz", "zst"},
		"exec":       {"exe", "msi", "bin", "app", "deb", "rpm", "sh", "bat", "cmd", "vbs", "ps1"},
		"scripts":    {"js", "ts", "py", "rb", "php", "go", "rs", "c", "cpp", "h", "java", "cs", "swift", "kt"},
		"config":     {"env", "config", "ini", "xml", "manifest", "lock", "gitignore", "editorconfig", "toml"},
		"telemetry":  {"log", "dump", "out", "err", "crash", "report", "audit"},
		"messaging":  {"eml", "msg", "vcf", "chat", "ics", "pbx"},
		"ui_assets":  {"ttf", "otf", "woff", "woff2", "eot", "sketch", "fig", "ai", "psd", "xd"},
		"simulation": {"obj", "stl", "fbx", "dwg", "dxf", "dae", "blend", "step", "iges", "glf", "gltf", "glb"},
		"backup_sys": {"bak", "sql", "snapshot", "dump", "db", "sqlite", "sqlite3", "rdb"},
	}

	for sector, exts := range sectorMap {
		for _, e := range exts {
			if e == ext {
				return sector
			}
		}
	}

	return "global"
}

// ParseBytes converts size string (e.g., "10MB") to bytes
func ParseBytes(sizeStr string) int64 {
	originalStr := sizeStr
	sizeStr = strings.ToUpper(strings.TrimSpace(sizeStr))

	// IMPORTANTE: Ordem deve ser do maior para o menor
	// para evitar que "B" seja detectado antes de "GB"/"MB"/"KB"
	multipliers := []struct {
		unit       string
		multiplier int64
	}{
		{"TB", 1024 * 1024 * 1024 * 1024},
		{"GB", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"KB", 1024},
		{"B",  1},
	}

	for _, m := range multipliers {
		if strings.HasSuffix(sizeStr, m.unit) {
			valStr := strings.TrimSuffix(sizeStr, m.unit)
			val, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				fmt.Printf("[ParseBytes ERROR] input='%s' (original='%s'), unit='%s', valStr='%s', err=%v\n",
					sizeStr, originalStr, m.unit, valStr, err)
				return 0
			}
			result := int64(val * float64(m.multiplier))
			fmt.Printf("[ParseBytes OK] input='%s' (original='%s'), unit='%s', val=%f, result=%d\n",
				sizeStr, originalStr, m.unit, val, result)
			return result
		}
	}

	// Try parsing as raw number
	val, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		fmt.Printf("[ParseBytes ERROR] input='%s' (original='%s'), raw parse err=%v\n",
			sizeStr, originalStr, err)
		return 0
	}
	fmt.Printf("[ParseBytes OK] input='%s' (original='%s'), raw number result=%d\n",
		sizeStr, originalStr, val)
	return val
}

// ResolveStorageConfig determines storage config based on governance rules
func ResolveStorageConfig(projectMetadata map[string]interface{}, ext string) *ProjectStorageConfig {
	// Get base config from metadata
	var baseConfig ProjectStorageConfig
	if configData, ok := projectMetadata["storage_config"]; ok {
		if configMap, ok := configData.(map[string]interface{}); ok {
			if provider, ok := configMap["provider"].(string); ok {
				baseConfig.Provider = StorageProviderType(provider)
			}
			// Parse provider-specific configs
			if s3Data, ok := configMap["s3"].(map[string]interface{}); ok {
				baseConfig.S3 = &S3Config{
					Bucket:          getString(s3Data, "bucket"),
					Region:          getString(s3Data, "region"),
					Endpoint:        getString(s3Data, "endpoint"),
					AccessKeyID:     getString(s3Data, "accessKeyId"),
					SecretAccessKey: getString(s3Data, "secretAccessKey"),
					PublicURLBase:   getString(s3Data, "publicUrlBase"),
				}
			}
			if cloudinaryData, ok := configMap["cloudinary"].(map[string]interface{}); ok {
				baseConfig.Cloudinary = &CloudinaryConfig{
					CloudName:    getString(cloudinaryData, "cloudName"),
					APIKey:       getString(cloudinaryData, "apiKey"),
					APISecret:    getString(cloudinaryData, "apiSecret"),
					UploadPreset: getString(cloudinaryData, "uploadPreset"),
				}
			}
			if imagekitData, ok := configMap["imagekit"].(map[string]interface{}); ok {
				baseConfig.ImageKit = &ImageKitConfig{
					PublicKey:   getString(imagekitData, "publicKey"),
					PrivateKey:  getString(imagekitData, "privateKey"),
					URLEndpoint: getString(imagekitData, "urlEndpoint"),
				}
			}
			if cloudflareData, ok := configMap["cloudflare"].(map[string]interface{}); ok {
				baseConfig.Cloudflare = &CloudflareConfig{
					AccountID: getString(cloudflareData, "accountId"),
					APIToken:  getString(cloudflareData, "apiToken"),
					Variant:   getString(cloudflareData, "variant"),
				}
			}
			if gdriveData, ok := configMap["gdrive"].(map[string]interface{}); ok {
				baseConfig.GDrive = &GDriveConfig{
					ClientEmail:  getString(gdriveData, "clientEmail"),
					PrivateKey:   getString(gdriveData, "privateKey"),
					RootFolderID: getString(gdriveData, "rootFolderId"),
				}
			}
			if dropboxData, ok := configMap["dropbox"].(map[string]interface{}); ok {
				baseConfig.Dropbox = &DropboxConfig{
					ClientID:     getString(dropboxData, "clientId"),
					ClientSecret: getString(dropboxData, "clientSecret"),
					RefreshToken: getString(dropboxData, "refreshToken"),
				}
			}
			if onedriveData, ok := configMap["onedrive"].(map[string]interface{}); ok {
				baseConfig.OneDrive = &OneDriveConfig{
					ClientID:     getString(onedriveData, "clientId"),
					ClientSecret: getString(onedriveData, "clientSecret"),
					RefreshToken: getString(onedriveData, "refreshToken"),
				}
			}
		}
	}

	// Check governance for sector-specific provider override
	sector := GetSectorForExt(ext)
	if governanceData, ok := projectMetadata["storage_governance"]; ok {
		if governance, ok := governanceData.(map[string]interface{}); ok {
			if sectorData, ok := governance[sector].(map[string]interface{}); ok {
				if providerOverride, ok := sectorData["storage_provider"].(string); ok && providerOverride != "" && providerOverride != "default" {
					baseConfig.Provider = StorageProviderType(providerOverride)
				}
			}
		}
	}

	// Default to local if not set
	if baseConfig.Provider == "" {
		baseConfig.Provider = ProviderLocal
	}

	return &baseConfig
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// GetSafePath ensures the target path is strictly within the root directory
// CORREÇÃO: Reforçado contra path traversal e caracteres especiais
func (s *StorageService) GetSafePath(root string, segments ...string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	cleanSegments := []string{}
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		
		// CORREÇÃO: Sanitização mais rigorosa
		// 1. Remover null bytes (ataque de path traversal via null byte injection)
		safe := strings.ReplaceAll(seg, "\x00", "")
		
		// 2. Remover .. de todas as formas possíveis (., .., ..., etc)
		safe = strings.ReplaceAll(safe, "..", "")
		
		// 3. Remover . no início (hidden files/paths)
		safe = strings.TrimLeft(safe, ".")
		
		// 4. Remover barras no início e fim
		safe = strings.Trim(safe, "/\\")
		
		// 5. Verificar se ficou vazio após sanitização
		if safe == "" {
			continue
		}
		
		// 6. Rejeitar nomes que começam com - (ataque via argumentos de comando)
		if strings.HasPrefix(safe, "-") {
			return "", fmt.Errorf("security violation: segment cannot start with '-': %s", seg)
		}
		
		// 7. Rejeitar caracteres de controle
		for _, r := range safe {
			if r < 32 {
				return "", fmt.Errorf("security violation: control characters not allowed: %s", seg)
			}
		}
		
		cleanSegments = append(cleanSegments, safe)
	}

	// CORREÇÃO: Verificar se temos segmentos válidos
	if len(cleanSegments) == 0 {
		return "", fmt.Errorf("security violation: no valid path segments")
	}

	target := filepath.Join(append([]string{absRoot}, cleanSegments...)...)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}

	// CORREÇÃO: Verificação mais rigorosa de path traversal
	sep := string(filepath.Separator)
	
	// Garantir que root termina com separador para comparação precisa
	if !strings.HasSuffix(absRoot, sep) {
		absRoot = absRoot + sep
	}
	
	// Verificação: target deve estar dentro de root
	if absTarget != strings.TrimSuffix(absRoot, sep) && !strings.HasPrefix(absTarget, absRoot) {
		return "", fmt.Errorf("security violation: path traversal detected (target: %s, root: %s)", absTarget, absRoot)
	}

	return absTarget, nil
}

// LocalUpload moves a file to its final destination
func (s *StorageService) LocalUpload(file io.Reader, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {  // CORREÇÃO: world não-readable
		return err
	}
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, file)
	return err
}

// --- Storage Indexer Functions ---

// StorageIndexerIndexerFunc type for indexing
type StorageIndexerIndexerFunc func(ctx context.Context, pool *pgxpool.Pool, projectSlug, bucket, relPath string, info map[string]interface{}) error

// StorageIndexerUnindexerFunc type for unindexing
type StorageIndexerUnindexerFunc func(ctx context.Context, pool *pgxpool.Pool, projectSlug, bucket, fullPath string) error

// StorageIndexerListerFunc type for listing
type StorageIndexerListerFunc func(ctx context.Context, pool *pgxpool.Pool, projectSlug, bucket, parentPath string) ([]map[string]interface{}, error)

// StorageIndexerSearcherFunc type for searching
type StorageIndexerSearcherFunc func(ctx context.Context, pool *pgxpool.Pool, projectSlug, query, bucket string) ([]map[string]interface{}, error)

// StorageIndexerSyncerFunc type for syncing
type StorageIndexerSyncerFunc func(ctx context.Context, pool *pgxpool.Pool, projectSlug, bucketName, storageRoot string) error

// IndexObject records a storage object in the system metadata
func IndexObject(ctx context.Context, pool *pgxpool.Pool, projectSlug, bucket, relPath string, info map[string]interface{}) error {
	branch := internalTypes.GetBranchName(ctx)
	sql := `
		INSERT INTO system.storage_objects (project_slug, branch_name, bucket, name, parent_path, full_path, size, mime_type, is_folder, provider, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		ON CONFLICT (project_slug, branch_name, bucket, full_path) 
		DO UPDATE SET size = EXCLUDED.size, mime_type = EXCLUDED.mime_type, updated_at = NOW(), provider = EXCLUDED.provider
	`

	name := filepath.Base(relPath)
	parentPath := filepath.Dir(relPath)
	if parentPath == "." {
		parentPath = ""
	}

	_, err := pool.Exec(ctx, sql,
		projectSlug, branch, bucket, name, parentPath, relPath,
		info["size"], info["mimeType"], info["isFolder"], info["provider"])
	return err
}

// escapeSQLLike escapa wildcards SQL para prevenir LIKE injection
func escapeSQLLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\") // Primeiro escape backslash
	s = strings.ReplaceAll(s, "%", "\\%")  // Escape %
	s = strings.ReplaceAll(s, "_", "\\_") // Escape _
	return s
}

// UnindexObject removes an object from the system metadata
func UnindexObject(ctx context.Context, pool *pgxpool.Pool, projectSlug, bucket, fullPath string) error {
	branch := internalTypes.GetBranchName(ctx)
	// CORREÇÃO: Escapar wildcards SQL para prevenir LIKE injection
	escapedPath := escapeSQLLike(fullPath)
	_, err := pool.Exec(ctx,
		"DELETE FROM system.storage_objects WHERE project_slug = $1 AND branch_name = $2 AND bucket = $3 AND (full_path = $4 OR full_path LIKE $5 ESCAPE '\\')",
		projectSlug, branch, bucket, fullPath, escapedPath+"/%")
	return err
}

// ListStorageObjects lists objects in a bucket/path
func ListStorageObjects(ctx context.Context, pool *pgxpool.Pool, projectSlug, bucket, parentPath string) ([]map[string]interface{}, error) {
	branch := internalTypes.GetBranchName(ctx)
	// Normalize path
	targetPath := parentPath
	if strings.HasSuffix(targetPath, "/") {
		targetPath = targetPath[:len(targetPath)-1]
	}
	if targetPath == "." {
		targetPath = ""
	}

	rows, err := pool.Query(ctx, `
		SELECT name, is_folder, size, updated_at, full_path 
		FROM system.storage_objects 
		WHERE project_slug = $1 AND branch_name = $2 AND bucket = $3 AND parent_path = $4
		ORDER BY is_folder DESC, name ASC
	`, projectSlug, branch, bucket, targetPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []map[string]interface{}
	for rows.Next() {
		var name, fullPath string
		var isFolder bool
		var size int64
		var updatedAt time.Time

		if err := rows.Scan(&name, &isFolder, &size, &updatedAt, &fullPath); err != nil {
			continue
		}

		itemType := "file"
		if isFolder {
			itemType = "folder"
		}

		items = append(items, map[string]interface{}{
			"name":       name,
			"type":       itemType,
			"size":       size,
			"updated_at": updatedAt,
			"path":       fullPath,
		})
	}

	return items, nil
}

// SearchStorageObjects searches for objects by name
func SearchStorageObjects(ctx context.Context, pool *pgxpool.Pool, projectSlug, query, bucket string) ([]map[string]interface{}, error) {
	branch := internalTypes.GetBranchName(ctx)
	sql := `
		SELECT name, is_folder, size, updated_at, full_path 
		FROM system.storage_objects 
		WHERE project_slug = $1 AND branch_name = $2 AND name ILIKE $3
	`
	params := []interface{}{projectSlug, branch, "%" + query + "%"}

	if bucket != "" {
		sql += " AND bucket = $4"
		params = append(params, bucket)
	}
	sql += " LIMIT 100"

	rows, err := pool.Query(ctx, sql, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []map[string]interface{}
	for rows.Next() {
		var name, fullPath string
		var isFolder bool
		var size int64
		var updatedAt time.Time

		if err := rows.Scan(&name, &isFolder, &size, &updatedAt, &fullPath); err != nil {
			continue
		}

		itemType := "file"
		if isFolder {
			itemType = "folder"
		}

		items = append(items, map[string]interface{}{
			"name":       name,
			"type":       itemType,
			"size":       size,
			"updated_at": updatedAt,
			"path":       fullPath,
		})
	}

	return items, nil
}

// SyncLocalBucket scans disk and syncs to database
func SyncLocalBucket(ctx context.Context, pool *pgxpool.Pool, projectSlug, bucketName, storageRoot string) error {
	branch := internalTypes.GetBranchName(ctx)
	bucketRoot := filepath.Join(storageRoot, projectSlug, bucketName)
	if branch != "main" {
		bucketRoot = filepath.Join(storageRoot, projectSlug, "branches", branch, bucketName)
	}


	var walkDir func(dir, relRoot string) error
	walkDir = func(dir, relRoot string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil // Skip errors
		}

		for _, entry := range entries {
			fullPathOnDisk := filepath.Join(dir, entry.Name())
			relPath := filepath.Join(relRoot, entry.Name())
			relPath = strings.ReplaceAll(relPath, "\\", "/")

			if entry.IsDir() {
				// Index as folder
				if err := IndexObject(ctx, pool, projectSlug, bucketName, relPath, map[string]interface{}{
					"size":     0,
					"mimeType": "application/directory",
					"isFolder": true,
					"provider": "local",
				}); err != nil {
					fmt.Printf("Warning: failed to index folder during sync: %v\n", err)
				}
				// Recurse
				walkDir(fullPathOnDisk, relPath)
			} else {
				// Index as file
				info, err := entry.Info()
				if err != nil {
					continue
				}
				if err := IndexObject(ctx, pool, projectSlug, bucketName, relPath, map[string]interface{}{
					"size":     info.Size(),
					"mimeType": "application/octet-stream",
					"isFolder": false,
					"provider": "local",
				}); err != nil {
					fmt.Printf("Warning: failed to index file during sync: %v\n", err)
				}
			}
		}
		return nil
	}

	return walkDir(bucketRoot, "")
}

// magicSignatures contains known file signatures for validation
// CORREÇÃO: Expandido para cobrir TODOS os formatos suportados pelo sistema
var magicSignatures = map[string][][]byte{
	// === VISUAL ===
	"jpg":   {{0xFF, 0xD8, 0xFF}},
	"jpeg":  {{0xFF, 0xD8, 0xFF}},
	"png":   {{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}},
	"gif":   {{0x47, 0x49, 0x46, 0x38}},
	"webp":  {{0x52, 0x49, 0x46, 0x46}}, // RIFF header
	"bmp":   {{0x42, 0x4D}},             // BM
	"tiff":  {{0x49, 0x49, 0x2A, 0x00}, {0x4D, 0x4D, 0x00, 0x2A}}, // II* ou MM*
	"ico":   {{0x00, 0x00, 0x01, 0x00}}, // Icon
	"svg":   {{0x3C, 0x3F, 0x78, 0x6D, 0x6C}, {0x3C, 0x73, 0x76, 0x67}}, // <?xml ou <svg
	"avif":  {{0x00, 0x00, 0x00, 0x20, 0x66, 0x74, 0x79, 0x70, 0x61, 0x76, 0x69, 0x66}}, // ftypavif
	"heic":  {{0x00, 0x00, 0x00, 0x20, 0x66, 0x74, 0x79, 0x70, 0x68, 0x65, 0x69, 0x63}}, // ftypheic
	"heif":  {{0x00, 0x00, 0x00, 0x20, 0x66, 0x74, 0x79, 0x70, 0x68, 0x65, 0x69, 0x66}}, // ftypheif

	// === MOTION ===
	"mp4":   {{0x00, 0x00, 0x00, 0x20, 0x66, 0x74, 0x79, 0x70}, {0x00, 0x00, 0x00, 0x18, 0x66, 0x74, 0x79, 0x70}, {0x00, 0x00, 0x00, 0x14, 0x66, 0x74, 0x79, 0x70}}, // ftyp
	"mov":   {{0x00, 0x00, 0x00, 0x14, 0x66, 0x74, 0x79, 0x70, 0x71, 0x74}}, // ftypqt
	"avi":   {{0x52, 0x49, 0x46, 0x46}}, // RIFF
	"mkv":   {{0x1A, 0x45, 0xDF, 0xA3}}, // EBML
	"webm":  {{0x1A, 0x45, 0xDF, 0xA3}}, // EBML
	"flv":   {{0x46, 0x4C, 0x56, 0x01}}, // FLV
	"wmv":   {{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11}}, // ASF
	"mpg":   {{0x00, 0x00, 0x01, 0xBA}, {0x00, 0x00, 0x01, 0xB3}}, // MPEG PS
	"mpeg":  {{0x00, 0x00, 0x01, 0xBA}, {0x00, 0x00, 0x01, 0xB3}},
	"3gp":   {{0x00, 0x00, 0x00, 0x20, 0x66, 0x74, 0x79, 0x70, 0x33, 0x67}}, // ftyp3g

	// === AUDIO ===
	"mp3":   {{0x49, 0x44, 0x33}, {0xFF, 0xFB}, {0xFF, 0xF3}, {0xFF, 0xF2}}, // ID3 ou MPEG sync
	"wav":   {{0x52, 0x49, 0x46, 0x46}}, // RIFF
	"flac":  {{0x66, 0x4C, 0x61, 0x43}}, // fLaC
	"ogg":   {{0x4F, 0x67, 0x67, 0x53}}, // OggS
	"m4a":   {{0x00, 0x00, 0x00, 0x20, 0x66, 0x74, 0x79, 0x70, 0x4D, 0x34, 0x41}}, // ftypM4A
	"wma":   {{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11}}, // ASF
	"amr":   {{0x23, 0x21, 0x41, 0x4D, 0x52}}, // #!AMR
	"opus":  {{0x4F, 0x67, 0x67, 0x53}}, // OggS (mesmo que ogg)
	"aac":   {{0xFF, 0xF1}, {0xFF, 0xF9}}, // ADTS
	"mid":   {{0x4D, 0x54, 0x68, 0x64}}, // MThd (MIDI)
	"midi":  {{0x4D, 0x54, 0x68, 0x64}},

	// === DOCS ===
	"pdf":   {{0x25, 0x50, 0x44, 0x46}}, // %PDF
	"doc":   {{0xD0, 0xCF, 0x11, 0xE0}}, // OLE Compound Document
	"docx":  {{0x50, 0x4B, 0x03, 0x04}}, // ZIP (Office Open XML)
	"odt":   {{0x50, 0x4B, 0x03, 0x04}}, // ZIP (ODF)
	"rtf":   {{0x7B, 0x5C, 0x72, 0x74, 0x66, 0x31}}, // {\rtf1
	"pages": {{0x50, 0x4B, 0x03, 0x04}}, // ZIP (iWork)
	"epub":  {{0x50, 0x4B, 0x03, 0x04}}, // ZIP (EPUB é ZIP)

	// === ARCHIVES ===
	"zip":   {{0x50, 0x4B, 0x03, 0x04}},
	"rar":   {{0x52, 0x61, 0x72, 0x21, 0x1A, 0x07, 0x00}, {0x52, 0x61, 0x72, 0x21, 0x1A, 0x07, 0x01, 0x00}}, // Rar!
	"7z":    {{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}}, // 7z
	"tar":   {{0x75, 0x73, 0x74, 0x61, 0x72}}, // ustar
	"gz":    {{0x1F, 0x8B}}, // gzip
	"bz2":   {{0x42, 0x5A, 0x68}}, // BZh
	"iso":   {{0x43, 0x44, 0x30, 0x30, 0x31}}, // CD001
	"dmg":   {{0x78, 0x01, 0x73, 0x0D, 0x62, 0x62, 0x60}}, // zlib compressed
	"xz":    {{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}}, // XZ
	"zst":   {{0x28, 0xB5, 0x2F, 0xFD}}, // Zstandard

	// === EXEC ===
	"exe":   {{0x4D, 0x5A}}, // MZ
	"msi":   {{0xD0, 0xCF, 0x11, 0xE0}}, // OLE
	"deb":   {{0x21, 0x3C, 0x61, 0x72, 0x63, 0x68, 0x3E}}, // !<arch>
	"rpm":   {{0xED, 0xAB, 0xEE, 0xDB}}, // RPM

	// === SCRIPTS ===
	"sh":    {{0x23, 0x21}}, // #! shebang
	"py":    {{0x23, 0x21}, {0xEF, 0xBB, 0xBF}, {0x63, 0x6C, 0x69, 0x63, 0x6B}}, // #!, BOM, ou 'click' (script)
	"rb":    {{0x23, 0x21}}, // #!
	"pl":    {{0x23, 0x21}}, // #!
	"php":   {{0x3C, 0x3F, 0x70, 0x68, 0x70}, {0x23, 0x21}}, // <?php ou #!
	"js":    {{0x23, 0x21}}, // #! (Node script)

	// === STRUCTURED ===
	"xls":   {{0xD0, 0xCF, 0x11, 0xE0}}, // OLE
	"xlsx":  {{0x50, 0x4B, 0x03, 0x04}}, // ZIP
	"ods":   {{0x50, 0x4B, 0x03, 0x04}}, // ZIP
	"parquet": {{0x50, 0x41, 0x52, 0x31}}, // PAR1

	// === UI ASSETS ===
	"ttf":   {{0x00, 0x01, 0x00, 0x00}, {0x74, 0x72, 0x75, 0x65, 0x00}}, // TrueType
	"otf":   {{0x4F, 0x54, 0x54, 0x4F}, {0x00, 0x01, 0x00, 0x00}}, // OTTO ou CFF
	"woff":  {{0x77, 0x4F, 0x46, 0x46}}, // wOFF
	"woff2": {{0x77, 0x4F, 0x46, 0x32}}, // wOF2
	"eot":   {{0x50, 0x4C, 0x53}}, // PCL (Embeded OpenType)
	"psd":   {{0x38, 0x42, 0x50, 0x53}}, // 8BPS

	// === SIMULATION ===
	"fbx":   {{0x4B, 0x61, 0x79, 0x64, 0x61, 0x72, 0x61}}, // Kaydara FBX
	"dae":   {{0x3C, 0x3F, 0x78, 0x6D, 0x6C}}, // <?xml (COLLADA é XML)
	"blend": {{0x42, 0x4C, 0x45, 0x4E, 0x44, 0x45, 0x52}}, // BLENDER
	"glb":   {{0x67, 0x6C, 0x54, 0x46}}, // glTF
	"gltf":  {{0x7B}}, // { (JSON)

	// === MESSAGING ===
	"eml":   {{0x52, 0x65, 0x74, 0x75, 0x72, 0x6E, 0x2D, 0x50}, {0x46, 0x72, 0x6F, 0x6D, 0x3A}}, // Return-Path ou From:

	// === BACKUP ===
	"sqlite":  {{0x53, 0x51, 0x4C, 0x69, 0x74, 0x65, 0x20, 0x66, 0x6F, 0x72, 0x6D, 0x61, 0x74}}, // SQLite format
	"sqlite3": {{0x53, 0x51, 0x4C, 0x69, 0x74, 0x65, 0x20, 0x66, 0x6F, 0x72, 0x6D, 0x61, 0x74}},
	"sql":     {{0x2D, 0x2D, 0x20, 0x53, 0x51, 0x4C}, {0x43, 0x52, 0x45, 0x41, 0x54, 0x45}, {0x49, 0x4E, 0x53, 0x45, 0x52, 0x54}}, // -- SQL, CREATE, INSERT
}

// dangerousExtensions contains executable/script extensions that require strict validation
var dangerousExtensions = map[string]bool{
	"exe": true, "msi": true, "bat": true, "cmd": true, "sh": true,
	"ps1": true, "vbs": true, "js": true, "jar": true, "com": true,
	"scr": true, "pif": true, "gadget": true, "wsf": true, "hta": true,
	"cpl": true, "msc": true, "inf": true, "reg": true, "dll": true,
	"py": true, "rb": true, "php": true, "pl": true, "awk": true,
}

// isDangerousExtension checks if extension is potentially executable/script
func isDangerousExtension(ext string) bool {
	ext = strings.ToLower(strings.TrimLeft(ext, "."))
	return dangerousExtensions[ext]
}

// ValidateMagicBytes validates file signature for security (from file)
// CORREÇÃO: Rejeita extensões perigosas sem magic bytes conhecidos
func ValidateMagicBytes(filePath string, ext string) bool {
	ext = strings.ToLower(strings.TrimLeft(ext, "."))

	validMagics, exists := magicSignatures[ext]
	if !exists {
		// CORREÇÃO: Se é uma extensão perigosa e não temos magic bytes, REJEITA
		if isDangerousExtension(ext) {
			fmt.Printf("[Security] Rejecting dangerous extension '%s' without magic bytes validation\n", ext)
			return false
		}
		// Extensão não perigosa e sem magic bytes - permite passar
		return true
	}

	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()

	buf := make([]byte, 16)
	n, err := file.Read(buf)
	if err != nil || n == 0 {
		return false
	}

	return validateMagicBuffer(buf, validMagics)
}

// ValidateMagicBytesBuffer validates file signature from buffer (streaming)
// CORREÇÃO: Rejeita extensões perigosas sem magic bytes conhecidos
func ValidateMagicBytesBuffer(buf []byte, ext string) bool {
	ext = strings.ToLower(strings.TrimLeft(ext, "."))

	validMagics, exists := magicSignatures[ext]
	if !exists {
		// CORREÇÃO: Se é uma extensão perigosa e não temos magic bytes, REJEITA
		if isDangerousExtension(ext) {
			fmt.Printf("[Security] Rejecting dangerous extension '%s' without magic bytes validation (buffer)\n", ext)
			return false
		}
		// Extensão não perigosa e sem magic bytes - permite passar
		return true
	}

	return validateMagicBuffer(buf, validMagics)
}

// validateMagicBuffer checks if buffer matches any of the magic signatures
func validateMagicBuffer(buf []byte, validMagics [][]byte) bool {
	for _, magic := range validMagics {
		if len(buf) >= len(magic) {
			match := true
			for i, b := range magic {
				if buf[i] != b {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}
