package settings

import "strings"

// Names of the helper containers a deployment can run next to the server.
// They follow the compose files shipped under deploy/services.
const runtimeNetwork = "riochat-network"

var runtimeImages = map[string]string{
	RuntimeTika:      "apache/tika:3.2.3.0",
	RuntimeDocling:   "riochat-docling:latest",
	RuntimeTesseract: "riochat-tesseract:latest",
	RuntimeRapidOCR:  "riochat-rapidocr:latest",
}

var runtimeLabels = map[string]string{
	RuntimeTika:      "Tika",
	RuntimeDocling:   "Docling",
	RuntimeTesseract: "Tesseract OCR",
	RuntimeRapidOCR:  "RapidOCR",
	RuntimeMinerU:    "MinerU",
}

// offlineRuntimeStatus describes a helper service from the saved settings alone,
// for when nothing manages it or it cannot be asked. The admin page can then
// still show where the service is expected and that it is not reachable, instead
// of failing to load.
func offlineRuntimeStatus(name string, ex ExtractRuntime) RuntimeStatus {
	st := RuntimeStatus{Source: "external", Status: "unhealthy"}
	switch name {
	case RuntimeTika:
		st.BaseURL = ex.TikaBaseURL
		if s := strings.TrimSpace(ex.TikaSource); s != "" {
			st.Source = s
		}
	case RuntimeDocling:
		st.BaseURL = ex.DoclingBaseURL
	case RuntimeTesseract:
		st.BaseURL = ex.TesseractBaseURL
	case RuntimeRapidOCR:
		st.BaseURL = ex.RapidOCRBaseURL
		if s := strings.TrimSpace(ex.RapidOCRSource); s != "" {
			st.Source = s
		}
	case RuntimeMinerU:
		st.BaseURL = ex.MinerUBaseURL
		st.Source = "cloud"
		if s := strings.TrimSpace(ex.MinerUSource); s != "" {
			st.Source = s
		}
	}
	if img, ok := runtimeImages[name]; ok {
		st.ContainerName = "riochat-" + name
		st.Image = img
		st.Network = runtimeNetwork
	}
	label := runtimeLabels[name]
	if label == "" {
		label = name
	}
	st.Message = "Cannot reach the " + label + " service. Check the service address and its authentication settings."
	return st
}
