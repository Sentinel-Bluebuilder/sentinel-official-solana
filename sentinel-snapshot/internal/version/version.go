package version

type Pins struct {
	SentinelHubVersion string
	CosmosSDKVersion   string
	IAVLVersion        string
	CometBFTVersion    string
	DBBackend          string
}

func Pinned() Pins {
	return Pins{
		SentinelHubVersion: "v12.0.2",
		CosmosSDKVersion:   "v0.47.17",
		IAVLVersion:        "v0.20.1",
		CometBFTVersion:    "v0.37.15",
		DBBackend:          "goleveldb",
	}
}
