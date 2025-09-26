package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"time"
)

// NodeUserData represents the user-data returned by the instance user-data endpoint
type NodeUserData struct {
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

func getNodeUserData() (NodeUserData, error) {
	// Get a new HTTP client using a priviledged port to get user-data endpoint
	client, err := createPrivilegedHTTPClient()
	if err != nil {
		return NodeUserData{}, fmt.Errorf("failed to create privileged HTTP client: %w", err)
	}

	// Get credentials from instance user-data
	resp, err := client.Get("http://169.254.42.42/user_data/k8s")
	if err != nil {
		return NodeUserData{}, fmt.Errorf("failed to get instance user-data: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return NodeUserData{}, fmt.Errorf("failed to get instance user-data: %v", resp.Status)
	}

	jsonNodeUserData, err := io.ReadAll(resp.Body)
	if err != nil {
		return NodeUserData{}, fmt.Errorf("failed to read instance user-data: %w", err)
	}

	err = resp.Body.Close()
	if err != nil {
		return NodeUserData{}, fmt.Errorf("failed to close response body: %w", err)
	}

	// Unmarshal the json user-data
	var nodeUserData NodeUserData
	err = json.Unmarshal(jsonNodeUserData, &nodeUserData)
	if err != nil {
		return NodeUserData{}, fmt.Errorf("failed to unmarshal instance user-data: %w", err)
	}

	return nodeUserData, nil
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
