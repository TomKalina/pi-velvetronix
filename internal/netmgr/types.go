package netmgr

// WifiStatus is the "wifi" object of the HTTP status (design §5).
// Nullable fields are pointers so they encode as JSON null.
type WifiStatus struct {
	Mode      string  `json:"mode"` // "sta" | "ap" | "connecting" | "off" | "unknown"
	SSID      *string `json:"ssid"`
	IP        *string `json:"ip"`
	Signal    *int    `json:"signal"` // 0..100
	APSSID    string  `json:"ap_ssid"`
	APIP      string  `json:"ap_ip"`
	LastError *string `json:"last_error"`
}

// Network is a visible Wi-Fi network.
type Network struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`
	Security string `json:"security"`
	InUse    bool   `json:"in_use"`
	Saved    bool   `json:"saved"`
}

// Saved is a saved NetworkManager Wi-Fi profile.
type Saved struct {
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	SSID     string `json:"ssid"`
	Priority int    `json:"priority"`
	Active   bool   `json:"active"`
}
