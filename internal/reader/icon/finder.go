// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package icon // import "miniflux.app/v2/internal/reader/icon"

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strings"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/crypto"
	"miniflux.app/v2/internal/http/client"
	"miniflux.app/v2/internal/logger"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/urllib"

	"github.com/PuerkitoBio/goquery"
)

// FindIcon try to find the website's icon.
func FindIcon(websiteURL, feedIconURL, userAgent string, fetchViaProxy, allowSelfSignedCertificates bool) (icon *model.Icon, err error) {
	if feedIconURL == "" {
		feedIconURL, err = fetchHTMLDocumentAndFindIconURL(websiteURL, userAgent, fetchViaProxy, allowSelfSignedCertificates)
		if err != nil {
			return nil, err
		}
	}

	if strings.HasPrefix(feedIconURL, "data:") {
		return parseImageDataURL(feedIconURL)
	}

	feedIconURL, err = generateIconURL(websiteURL, feedIconURL)
	if err != nil {
		return nil, err
	}

	if icon, err = downloadIcon(feedIconURL, userAgent, fetchViaProxy, allowSelfSignedCertificates); err != nil {
		return nil, err
	}

	return icon, nil
}

func generateIconURL(websiteURL, feedIconURL string) (iconURL string, err error) {
	feedIconURL = strings.TrimSpace(feedIconURL)

	if feedIconURL == "" {
		iconURL, err = urllib.JoinBaseURLAndPath(urllib.RootURL(websiteURL), "favicon.ico")
		if err != nil {
			return "", fmt.Errorf(`icon: unable to join base URL and path: %w`, err)
		}
	} else {
		iconURL, err = urllib.AbsoluteURL(websiteURL, feedIconURL)
		if err != nil {
			return "", fmt.Errorf(`icon: unable to convert icon URL to absolute URL: %w`, err)
		}
	}

	return iconURL, nil
}

func fetchHTMLDocumentAndFindIconURL(websiteURL, userAgent string, fetchViaProxy, allowSelfSignedCertificates bool) (string, error) {
	rootURL := urllib.RootURL(websiteURL)
	logger.Debug("[FindIcon] Find icon from HTML webpage: %s", rootURL)

	clt := client.NewClientWithConfig(rootURL, config.Opts)
	clt.WithUserAgent(userAgent)
	clt.AllowSelfSignedCertificates = allowSelfSignedCertificates

	if fetchViaProxy {
		clt.WithProxy()
	}

	response, err := clt.Get()
	if err != nil {
		return "", fmt.Errorf("icon: unable to download website index page: %v", err)
	}

	if response.HasServerFailure() {
		return "", fmt.Errorf("icon: unable to download website index page: status=%d", response.StatusCode)
	}

	return findIconURLFromHTMLDocument(response.Body)
}

func findIconURLFromHTMLDocument(body io.Reader) (string, error) {
	queries := []string{
		"link[rel='shortcut icon']",
		"link[rel='Shortcut Icon']",
		"link[rel='icon shortcut']",
		"link[rel='icon']",
	}

	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return "", fmt.Errorf("icon: unable to read document: %v", err)
	}

	var iconURL string
	for _, query := range queries {
		doc.Find(query).Each(func(i int, s *goquery.Selection) {
			if href, exists := s.Attr("href"); exists {
				iconURL = strings.TrimSpace(href)
			}
		})

		if iconURL != "" {
			break
		}
	}

	return iconURL, nil
}

func downloadIcon(iconURL, userAgent string, fetchViaProxy, allowSelfSignedCertificates bool) (*model.Icon, error) {
	clt := client.NewClientWithConfig(iconURL, config.Opts)
	clt.WithUserAgent(userAgent)
	clt.AllowSelfSignedCertificates = allowSelfSignedCertificates
	if fetchViaProxy {
		clt.WithProxy()
	}

	response, err := clt.Get()
	if err != nil {
		return nil, fmt.Errorf("icon: unable to download iconURL: %v", err)
	}

	if response.HasServerFailure() {
		return nil, fmt.Errorf("icon: unable to download icon: status=%d", response.StatusCode)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("icon: unable to read downloaded icon: %v", err)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("icon: downloaded icon is empty, iconURL=%s", iconURL)
	}

	icon := &model.Icon{
		Hash:     crypto.HashFromBytes(body),
		MimeType: response.ContentType,
		Content:  body,
	}

	return icon, nil
}

// https://developer.mozilla.org/en-US/docs/Web/HTTP/Basics_of_HTTP/Data_URIs#syntax
// data:[<mediatype>][;base64],<data>
func parseImageDataURL(value string) (*model.Icon, error) {
	var mediaType string
	var encoding string

	if !strings.HasPrefix(value, "data:") {
		return nil, fmt.Errorf(`icon: invalid data URL (missing data:) %q`, value)
	}

	value = value[5:]

	comma := strings.Index(value, ",")
	if comma < 0 {
		return nil, fmt.Errorf(`icon: invalid data URL (no comma) %q`, value)
	}

	data := value[comma+1:]
	semicolon := strings.Index(value[0:comma], ";")

	if semicolon > 0 {
		mediaType = value[0:semicolon]
		encoding = value[semicolon+1 : comma]
	} else {
		mediaType = value[0:comma]
	}

	if !strings.HasPrefix(mediaType, "image/") {
		return nil, fmt.Errorf(`icon: invalid media type %q`, mediaType)
	}

	var blob []byte
	switch encoding {
	case "base64":
		var err error
		blob, err = base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf(`icon: invalid data %q (%v)`, value, err)
		}
	case "":
		decodedData, err := url.QueryUnescape(data)
		if err != nil {
			return nil, fmt.Errorf(`icon: unable to decode data URL %q`, value)
		}
		blob = []byte(decodedData)
	default:
		return nil, fmt.Errorf(`icon: unsupported data URL encoding %q`, value)
	}

	if len(blob) == 0 {
		return nil, fmt.Errorf(`icon: empty data URL %q`, value)
	}

	icon := &model.Icon{
		Hash:     crypto.HashFromBytes(blob),
		Content:  blob,
		MimeType: mediaType,
	}

	return icon, nil
}
