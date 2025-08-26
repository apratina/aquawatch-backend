package internal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jung-kurt/gofpdf"
)

// ReportItem represents a single anomalous site row in the PDF table.
type ReportItem struct {
	Site           string  `json:"site"`
	Reason         string  `json:"reason"`
	PredictedValue float64 `json:"predicted_value"`
	AnomalyDate    string  `json:"anomaly_date"`
}

// GenerateReportPDF produces a PDF with image on the left and a table on the right.
// If FOXIT_API_URL and FOXIT_API_KEY are set, it attempts to use Foxit API; otherwise
// it falls back to a local generator.
func GenerateReportPDF(ctx context.Context, imageBytes []byte, items []ReportItem) ([]byte, error) {
	// Prefer Foxit when client credentials are configured
	if os.Getenv("FOXIT_CLIENT_ID") != "" && os.Getenv("FOXIT_CLIENT_SECRET") != "" {
		log.Println("using foxit api")
		b, err := generateWithFoxit(ctx, imageBytes, items)
		if err == nil {
			return b, nil
		}
		log.Println("foxit api error:", err)
		// fall back to local on error
	}
	log.Println("using local generator")
	return generateWithLocal(imageBytes, items)
}

// generateWithFoxit posts multipart data to a configurable Foxit generation endpoint.
// The concrete API contract can vary; we send image and a JSON layout description.
func generateWithFoxit(ctx context.Context, imageBytes []byte, items []ReportItem) ([]byte, error) {
	url := os.Getenv("FOXIT_API_URL")
	if url == "" {
		url = "https://na1.fusion.foxit.com/pdf-services/api/documents/create/pdf-from-html"
	}
	uploadURL := os.Getenv("FOXIT_UPLOAD_URL")
	if uploadURL == "" {
		uploadURL = "https://na1.fusion.foxit.com/pdf-services/api/documents/upload"
	}
	tasksBase := os.Getenv("FOXIT_TASKS_URL")
	if tasksBase == "" {
		tasksBase = "https://na1.fusion.foxit.com/pdf-services/api/tasks"
	}
	downloadBase := os.Getenv("FOXIT_DOWNLOAD_URL")
	if downloadBase == "" {
		downloadBase = "https://na1.fusion.foxit.com/pdf-services/api/documents"
	}
	apiKey := os.Getenv("FOXIT_CLIENT_ID")
	apiSecret := os.Getenv("FOXIT_CLIENT_SECRET")
	if apiKey == "" || apiSecret == "" {
		return nil, errors.New("foxit api not configured")
	}

	// Build HTML payload with embedded image and table
	contentType := http.DetectContentType(imageBytes)
	if contentType == "application/octet-stream" {
		contentType = "image/png"
	}
	imgB64 := base64.StdEncoding.EncodeToString(imageBytes)
	dataURL := fmt.Sprintf("data:%s;base64,%s", contentType, imgB64)

	var rows bytes.Buffer
	for _, it := range items {
		rows.WriteString("<tr>")
		rows.WriteString("<td>" + htmlEscape(it.AnomalyDate) + "</td>")
		rows.WriteString("<td>" + htmlEscape(it.Site) + "</td>")
		rows.WriteString("<td>" + htmlEscape(it.Reason) + "</td>")
		rows.WriteString(fmt.Sprintf("<td>%.2f</td>", it.PredictedValue))
		rows.WriteString("</tr>")
	}

	html := `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8" />
  <title>Anomaly Report</title>
  <style>
    body { font-family: Arial, sans-serif; }
    h1 { text-align: center; margin: 12px 0; }
    .img { width: 100%; margin: 8px 0 12px 0; }
    table { width: 100%; border-collapse: collapse; }
    th, td { border: 1px solid #333; padding: 6px; font-size: 12px; }
    th { background: #f0f0f0; text-align: left; }
  </style>
</head>
<body>
  <h1>Anomaly Report</h1>
  <img class="img" src="` + dataURL + `" />
  <table>
    <thead>
      <tr>
        <th>Date</th>
        <th>Site</th>
        <th>Reason</th>
        <th>Predicted</th>
      </tr>
    </thead>
    <tbody>
      ` + rows.String() + `
    </tbody>
  </table>
</body>
</html>`

	// 1) Upload HTML to get documentId
	docID, upErr := foxitUploadHTML(ctx, uploadURL, apiKey, apiSecret, html)
	if upErr != nil || docID == "" {
		return nil, fmt.Errorf("foxit upload failed: %v", upErr)
	}
	log.Println("foxit upload success:", docID)

	// 2) Start create task and get taskId
	taskID, err := foxitStartCreateTask(ctx, url, apiKey, apiSecret, docID)
	if err != nil {
		return nil, fmt.Errorf("foxit start task failed: %w", err)
	}

	// 3) Poll task until COMPLETED
	status, resultDocumentID, err := foxitPollTask(ctx, tasksBase, apiKey, apiSecret, taskID, 20*time.Second, 3*time.Second)
	if err != nil {
		return nil, err
	}
	if status != "COMPLETED" {
		return nil, fmt.Errorf("foxit task not completed: %s", status)
	}
	if resultDocumentID == "" {
		return nil, errors.New("foxit task completed without download url")
	}

	// 4) Download PDF bytes
	return foxitDownload(ctx, downloadBase, resultDocumentID, apiKey, apiSecret)
}

// foxitStartCreateTask posts {documentId} to the create endpoint and returns a taskId.
func foxitStartCreateTask(ctx context.Context, createURL, clientID, clientSecret, documentID string) (string, error) {
	body, _ := json.Marshal(map[string]string{"documentId": documentID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("client_id", clientID)
	req.Header.Set("client_secret", clientSecret)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("foxit create error: %s", string(b))
	}
	var out struct {
		TaskID string `json:"taskId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.TaskID == "" {
		return "", errors.New("missing taskId in response")
	}
	return out.TaskID, nil
}

// foxitPollTask polls task status until completion or timeout. Returns status and a download URL if available.
func foxitPollTask(ctx context.Context, tasksBase, clientID, clientSecret, taskID string, timeout time.Duration, interval time.Duration) (string, string, error) {
	deadline := time.Now().Add(timeout)
	taskURL := strings.TrimRight(tasksBase, "/") + "/" + taskID
	for {
		if time.Now().After(deadline) {
			return "TIMEOUT", "", errors.New("foxit task poll timeout")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, taskURL, nil)
		if err != nil {
			return "FAILED", "", err
		}
		req.Header.Set("client_id", clientID)
		req.Header.Set("client_secret", clientSecret)
		resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			time.Sleep(interval)
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			b, _ := io.ReadAll(resp.Body)
			return "FAILED", "", fmt.Errorf("foxit task status error: %s", string(b))
		}
		var out struct {
			Status           string `json:"status"`
			ResultDocumentID string `json:"resultDocumentId"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return "FAILED", "", err
		}
		status := strings.ToUpper(out.Status)
		switch status {
		case "COMPLETED":
			return status, out.ResultDocumentID, nil
		case "FAILED", "ERROR", "CANCELLED":
			return status, "", fmt.Errorf("foxit task failed: %s", status)
		}
		time.Sleep(interval)
	}
}

// foxitDownload fetches the generated PDF bytes from the given URL.
func foxitDownload(ctx context.Context, downloadBase, resultDocumentID, clientID, clientSecret string) ([]byte, error) {
	downloadURL := fmt.Sprintf("%s/%s/download", downloadBase, resultDocumentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("client_id", clientID)
	req.Header.Set("client_secret", clientSecret)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("foxit download error: %s", string(b))
	}
	return io.ReadAll(resp.Body)
}

func htmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(s)
}

// foxitUploadHTML uploads HTML content as a document and returns its documentId.
func foxitUploadHTML(ctx context.Context, uploadURL, apiKey, apiSecret, html string) (string, error) {
	// Build multipart/form-data request: field name must be 'file' containing the HTML
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "report.html")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, strings.NewReader(html)); err != nil {
		return "", err
	}
	_ = mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("client_id", apiKey)
	req.Header.Set("client_secret", apiSecret)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("foxit upload error: %s", string(b))
	}
	var out struct {
		DocumentID string `json:"documentId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.DocumentID == "" {
		return "", errors.New("missing documentId in response")
	}
	return out.DocumentID, nil
}

// generateWithLocal draws a PDF with title on top, image below, and table under the image.
func generateWithLocal(imageBytes []byte, items []ReportItem) ([]byte, error) {
	// Validate image decodability early
	imgDecoded, _, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, fmt.Errorf("invalid image: %w", err)
	}

	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	// Page metrics
	pageW, _ := pdf.GetPageSize()
	left, top, right, _ := pdf.GetMargins()
	usableW := pageW - left - right

	// Title at the top, centered
	pdf.SetFont("Arial", "B", 18)
	pdf.SetXY(left, top)
	pdf.CellFormat(usableW, 10, "Anomaly Report", "", 1, "C", false, 0, "")

	// Image below title, full width
	contentType := http.DetectContentType(imageBytes)
	imgType := "PNG"
	if strings.Contains(strings.ToLower(contentType), "jpeg") || strings.Contains(strings.ToLower(contentType), "jpg") {
		imgType = "JPG"
	}
	bounds := imgDecoded.Bounds()
	aspect := float64(bounds.Dy()) / float64(bounds.Dx())
	imgW := usableW
	imgH := imgW * aspect
	y := top + 12
	imgName := fmt.Sprintf("img-%d", time.Now().UnixNano())
	pdf.RegisterImageOptionsReader(imgName, gofpdf.ImageOptions{ImageType: imgType}, bytes.NewReader(imageBytes))
	pdf.ImageOptions(imgName, left, y, imgW, imgH, false, gofpdf.ImageOptions{ImageType: imgType}, 0, "")
	y += imgH + 8

	// Table below image (Date, Site, Reason, Predicted)
	pdf.SetFont("Arial", "B", 11)
	headers := []string{"Date", "Site", "Reason", "Predicted"}
	widths := []float64{usableW * 0.15, usableW * 0.2, usableW * 0.5, usableW * 0.15}
	x := left
	for i, h := range headers {
		pdf.Rect(x, y-5, widths[i], 8, "D")
		pdf.Text(x+2, y, h)
		x += widths[i]
	}
	// Rows
	pdf.SetFont("Arial", "", 10)
	y += 10
	for _, it := range items {
		x = left
		cells := []string{
			it.AnomalyDate,
			it.Site,
			it.Reason,
			fmt.Sprintf("%.2f", it.PredictedValue),
		}
		rowH := 8.0
		for i, c := range cells {
			pdf.Rect(x, y-5, widths[i], rowH, "D")
			pdf.SetXY(x+2, y-3)
			pdf.MultiCell(widths[i]-4, 5, c, "", "L", false)
			x += widths[i]
		}
		y += rowH + 2
	}

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
