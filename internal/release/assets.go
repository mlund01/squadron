package release

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Size        int64  `json:"size"`
}

func ListAssets(src GitHubSource, version string) ([]Asset, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s",
		src.Owner, src.Repo, version)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github api %s: %s: %s", url, resp.Status, string(body))
	}

	var payload struct {
		Assets []Asset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode github api response: %w", err)
	}
	return payload.Assets, nil
}
