package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"slices"
	"time"
)

// NodeUserData represents the user-data returned by the instance user-data endpoint
type UserData struct {
	MetadataURL   string `json:"metadata_url"`
	NodeSecretKey string `json:"node_secret_key"`
}

// NodeMetadata represents the metadata returned by the node metadata endpoint
type NodeMetadata struct {
	ID                       string            `json:"id"`
	Name                     string            `json:"name"`
	ClusterURL               string            `json:"cluster_url"`
	ClusterCA                string            `json:"cluster_ca"`
	CredentialProviderConfig string            `json:"credential_provider_config"`
	PoolVersion              string            `json:"pool_version"`
	KubeletConfig            string            `json:"kubelet_config"`
	NodeLabels               map[string]string `json:"node_labels"`
	NodeTaints               []struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Effect string `json:"effect"`
	} `json:"node_taints"`
	ProviderID     string            `json:"provider_id"`
	ResolvconfPath string            `json:"resolvconf_path"`
	TemplateArgs   map[string]string `json:"template_args"`

	RepoURI string `json:"repo_uri"`
	Token   string // Token is not part of the metadata, it is get from the instance user-data

	// Kapsule-specific fields
	HasGPU bool `json:"has_gpu"`

	// Kosmos-specific fields
	ExternalIP string `json:"external_ip"`

	// Installer tags
	InstallerTags []string `json:"installer_tags"`
}

func getNodeUserData() (UserData, error) {
	// Get a new HTTP client using a priviledged port to get user-data endpoint
	client, err := createPrivilegedHTTPClient()
	if err != nil {
		return UserData{}, fmt.Errorf("failed to create privileged HTTP client: %w", err)
	}

	// Get credentials from instance user-data
	resp, err := client.Get("http://169.254.42.42/user_data/k8s")
	if err != nil {
		return UserData{}, fmt.Errorf("failed to get instance user-data: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return UserData{}, fmt.Errorf("failed to get instance user-data: %v", resp.Status)
	}

	jsonNodeUserData, err := io.ReadAll(resp.Body)
	if err != nil {
		return UserData{}, fmt.Errorf("failed to read instance user-data: %w", err)
	}

	err = resp.Body.Close()
	if err != nil {
		return UserData{}, fmt.Errorf("failed to close response body: %w", err)
	}

	// Unmarshal the json user-data
	var userData UserData
	err = json.Unmarshal(jsonNodeUserData, &userData)
	if err != nil {
		return UserData{}, fmt.Errorf("failed to unmarshal instance user-data: %w", err)
	}

	return userData, nil
}

func getKosmosUserData() (UserData, error) {
	envPoolID := os.Getenv("POOL_ID")
	if len(envPoolID) == 0 {
		return UserData{}, errors.New("POOL_ID env var must be set when using Kosmos mode")
	}

	envPoolRegion := os.Getenv("POOL_REGION")
	if len(envPoolRegion) == 0 {
		return UserData{}, errors.New("POOL_REGION env var must be set when using Kosmos mode")
	}

	envSecretKey := os.Getenv("SCW_SECRET_KEY")
	if len(envSecretKey) == 0 {
		return UserData{}, errors.New("SCW_SECRET_KEY env var must be set when using Kosmos mode")
	}

	// Set default Scaleway API URL and allow to override it for dev purposes
	apiURL := "https://api.scaleway.com"
	if len(os.Getenv("SCW_API_URL")) > 0 {
		apiURL = os.Getenv("SCW_API_URL")
	}

	// Kosmos node needs to register to get its token
	userData, err := registerKosmosNode(apiURL, envPoolID, envPoolRegion, envSecretKey)
	if err != nil {
		return UserData{}, fmt.Errorf("failed to register Kosmos node: %w", err)
	}

	return userData, nil
}

// registerKosmosNode registers a new Kosmos node in the pool and retrieve its generated token
func registerKosmosNode(apiURL, poolID, poolRegion, secretKey string) (UserData, error) {
	userdataCachePath := "/etc/scw-k8s-userdata"

	// If userdata cache file is present it means the node is already registered, so use it
	userdataCache, err := os.ReadFile(userdataCachePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) { // If the userdata cache file is not found, it means the node is not registered, so ignore the error and continue with registration
		return UserData{}, fmt.Errorf("failed to read userdata cache: %w", err)
	} else if err == nil {
		// Userdata cache file found, use it and skip registration
		var userData UserData
		err = json.Unmarshal(userdataCache, &userData)
		if err != nil {
			return UserData{}, fmt.Errorf("failed to unmarshal Kosmos node userdata cache: %w", err)
		}
		return userData, nil
	}

	// Create a new HTTP client to get the node metadata
	client := &http.Client{Timeout: 10 * time.Second}

	// Create a new request with the header X-Auth-Token set to the user secret key
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/k8s/v1/regions/%s/pools/%s/external-nodes/auth", apiURL, poolRegion, poolID), nil)
	if err != nil {
		return UserData{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Auth-Token", secretKey)

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return UserData{}, fmt.Errorf("failed to register Kosmos node: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return UserData{}, fmt.Errorf("failed to register Kosmos node: %v", resp.Status)
	}

	// Read and close the body of the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return UserData{}, fmt.Errorf("failed to read Kosmos node registration response: %w", err)
	}

	err = resp.Body.Close()
	if err != nil {
		return UserData{}, fmt.Errorf("failed to close Kosmos node registration body: %w", err)
	}

	// Unmarshal the json registration response
	var userData UserData
	err = json.Unmarshal(body, &userData)
	if err != nil {
		return UserData{}, fmt.Errorf("failed to unmarshal Kosmos node registration response: %w", err)
	}

	// Write the registration response to the userdata cache, so the registration can be skipped next time
	err = os.WriteFile(userdataCachePath, body, 0600)
	if err != nil {
		return UserData{}, fmt.Errorf("failed to write Kosmos node userdata cache: %w", err)
	}

	return userData, nil
}

func getNodeMetadata(url, token string) (NodeMetadata, error) {
	// Create a new HTTP client to get the node metadata
	client := &http.Client{Timeout: 10 * time.Second}

	// Create a new request with the header X-Auth-Token set to the node token
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return NodeMetadata{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Auth-Token", token)

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return NodeMetadata{}, fmt.Errorf("failed to get node metadata: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return NodeMetadata{}, fmt.Errorf("failed to get node metadata: %v", resp.Status)
	}

	// Read and close the body of the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return NodeMetadata{}, fmt.Errorf("failed to read node metadata: %w", err)
	}

	err = resp.Body.Close()
	if err != nil {
		return NodeMetadata{}, fmt.Errorf("failed to close response body: %w", err)
	}

	// Unmarshal the json metadata
	var metadata NodeMetadata
	err = json.Unmarshal(body, &metadata)
	if err != nil {
		return NodeMetadata{}, fmt.Errorf("failed to unmarshal node metadata: %w", err)
	}

	metadata.Token = token

	return metadata, nil
}

func createPrivilegedHTTPClient() (*http.Client, error) {
	// Exclude some ports from that can cause conflicts with other services running or to be run on the node
	excludedPorts := []int{
		179, // Used by calico-bird BGP.
	}

	// Find a free priviledged port to use for the HTTP client
	var clientPrivilegedPort int
	for i := 1; i < 1024; i++ {
		// Skip the excluded ports
		if slices.Contains(excludedPorts, i) {
			continue
		}

		// Try to bind to the port
		laddr := &net.TCPAddr{Port: i}
		conn, err := net.ListenTCP("tcp", laddr)
		if err == nil {
			clientPrivilegedPort = i
			err = conn.Close()
			if err != nil {
				return nil, fmt.Errorf("failed to close connection: %w", err)
			}
			break
		}
	}
	if clientPrivilegedPort == 0 {
		return nil, fmt.Errorf("failed to get a priviledged port")
	}

	// Create a new HTTP client using the priviledged port
	return &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: -1,
				LocalAddr: &net.TCPAddr{Port: clientPrivilegedPort},
			}).DialContext,
		},
	}, nil
}
