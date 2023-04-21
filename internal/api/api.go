// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/twitchdev/twitch-cli/internal/login"
	"github.com/twitchdev/twitch-cli/internal/models"
	"github.com/twitchdev/twitch-cli/internal/util"

	"github.com/TylerBrock/colorjson"
	"github.com/fatih/color"
	"github.com/spf13/viper"
)

var baseURL = "https://api.twitch.tv/helix"

type clientInformation struct {
	ClientID string
	Token    string
}

// NewRequest is used to request data from the Twitch API using a HTTP GET request- this function is a wrapper for the apiRequest function that handles the network call
func NewRequest(method string, path string, queryParameters []string, body []byte, prettyPrint bool, autopaginate *int) {
	var data models.APIResponse
	var err error
	var cursor string

	isExtensionsLiveEndpoint := false // https://github.com/twitchdev/twitch-cli/issues/157

	data.Data = make([]interface{}, 0)
	client, err := GetClientInformation()
	if err != nil {
		fmt.Println("Error fetching client information", err.Error())
		return
	}

	if autopaginate != nil && *autopaginate < 0 {
		fmt.Println("Invalid pagination value provided. Must be greater than or equal to 0.")
		return
	}

	if viper.GetString("BASE_URL") != "" {
		baseURL = viper.GetString("BASE_URL")
	}

	runCounter := 1
	for {
		var apiResponse models.APIResponse

		u, err := url.Parse(baseURL + path)
		if err != nil {
			fmt.Printf("Error getting url: %v", err)
			return
		}

		q := u.Query()
		for _, paramStr := range queryParameters {
			var value string
			param := strings.Split(paramStr, "=")
			if len(param) == 2 {
				value = param[1]
			}
			q.Add(param[0], value)
		}

		if cursor != "" {
			q.Set("after", cursor)
		}

		if autopaginate != nil {
			first := "100"
			// since channel points custom rewards endpoints only support 50, capping that here
			if strings.Contains(u.String(), "custom_rewards") {
				first = "50"
			}

			q.Set("first", first)
		}

		u.RawQuery = q.Encode()

		resp, err := apiRequest(strings.ToUpper(method), u.String(), body, apiRequestParameters{
			ClientID: client.ClientID,
			Token:    client.Token,
		})
		if err != nil {
			fmt.Printf("Error reading body: %v", err)
			return
		}

		if resp.StatusCode == http.StatusNoContent {
			fmt.Println("Endpoint responded with status 204")
			return
		}
		if strings.Contains(u.String(), "extensions/live") {
			// https://github.com/twitchdev/twitch-cli/issues/157
			isExtensionsLiveEndpoint = true
			var extensionsBody models.ExtensionAPIResponse
			err = json.Unmarshal(resp.Body, &extensionsBody)
			if err != nil {
				fmt.Printf("Error unmarshalling body: %v", err)
				return
			}

			var cursor string = ""
			if extensionsBody.Pagination != nil {
				cursor = *extensionsBody.Pagination
			}

			apiResponse = models.APIResponse{
				Data:    extensionsBody.Data,
				Status:  extensionsBody.Status,
				Error:   extensionsBody.Error,
				Message: extensionsBody.Message,
				Pagination: &models.APIPagination{
					Cursor: cursor,
				},
			}
		} else {
			err = json.Unmarshal(resp.Body, &apiResponse)
			if err != nil {
				fmt.Printf("Error unmarshalling body: %v", err)
				return
			}
		}

		if runCounter == 1 {
			data = apiResponse
		}

		if resp.StatusCode > 299 || resp.StatusCode < 200 {
			data = apiResponse
			break
		}

		if apiResponse.Data == nil {
			break
		}

		if strings.Contains(path, "schedule") || data.Data == nil {
			data.Data = apiResponse.Data
			break // autopagination unsupported
		} else if runCounter > 1 {
			data.Data = append(data.Data.([]interface{}), apiResponse.Data.([]interface{})...)
		}

		if apiResponse.Pagination == nil || apiResponse.Pagination.Cursor == "" {
			break
		}

		if autopaginate == nil {
			data.Pagination = &models.APIPagination{
				Cursor: apiResponse.Pagination.Cursor,
			}
			break
		}

		if apiResponse.Pagination.Cursor == cursor {
			break
		}
		cursor = apiResponse.Pagination.Cursor

		// if autopaginate is 0, run indefinitely. otherwise, track counter and break once met limit
		if *autopaginate != 0 && *autopaginate <= runCounter {
			break // break if
		}
		runCounter++
	}

	if data.Data == nil {
		data.Data = make([]interface{}, 0)
	}
	// handle json marshalling better; returns empty slice vs. null
	_, isInterface := data.Data.([]interface{})
	if isInterface && !strings.Contains(path, "schedule") && len(data.Data.([]interface{})) == 0 && data.Error == "" {
		data.Data = make([]interface{}, 0)
	}
	_, isStringMap := data.Data.(map[string]any)
	if isStringMap && !strings.Contains(path, "schedule") && len(data.Data.(map[string]any)) == 0 && data.Error == "" {
		data.Data = make(map[string]any, 0)
	}

	var d []byte
	if isExtensionsLiveEndpoint {
		extensionBody := models.ExtensionAPIResponse{
			Data:       data.Data,
			Pagination: &data.Pagination.Cursor,
			Error:      data.Error,
			Status:     data.Status,
			Message:    data.Message,
		}
		d, err = json.Marshal(extensionBody)
		if err != nil {
			log.Printf("Error marshalling json: %v", err)
			return
		}
	} else {
		d, err = json.Marshal(data)
		if err != nil {
			log.Printf("Error marshalling json: %v", err)
			return
		}
	}

	if prettyPrint {
		var obj map[string]interface{}
		json.Unmarshal(d, &obj)
		// since Command Prompt/Powershell don't support coloring, will pretty print without colors
		if runtime.GOOS == "windows" {
			s, _ := json.MarshalIndent(obj, "", "  ")
			fmt.Println(string(s))
			return
		}

		f := colorjson.NewFormatter()
		f.Indent = 2
		f.KeyColor = color.New(color.FgBlue).Add(color.Bold)
		s, err := f.Marshal(obj)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(string(s))
		return
	}

	fmt.Println(string(d))
}

// ValidOptions returns a list of supported endpoints given a specified method as noted in the map endpointMethodSupports, which is located in resources.go of this package.
func ValidOptions(method string) []string {
	names := []string{}

	for n, m := range endpointMethodSupports {
		if m[method] {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	return names
}

func GetClientInformation() (clientInformation, error) {
	clientID := viper.GetString("clientID")
	expiration := viper.GetString("tokenexpiration")
	token := viper.GetString("accessToken")

	// Handle legacy nonexpiring tokens
	if expiration == "0" {
		return clientInformation{
			Token:    token,
			ClientID: clientID,
		}, nil
	}

	ex, _ := time.Parse(time.RFC3339Nano, expiration)
	if ex.Before(util.GetTimestamp()) {
		refreshToken := viper.GetString("refreshToken")

		if refreshToken == "" {
			log.Fatal("Please run twitch token")
		}

		clientSecret := viper.GetString("clientSecret")

		var err error
		r, err := login.RefreshUserToken(login.RefreshParameters{
			RefreshToken: refreshToken,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			URL:          login.RefreshTokenURL,
		})
		if err != nil {
			return clientInformation{}, err
		}
		token = r.Response.AccessToken
	}

	return clientInformation{Token: token, ClientID: clientID}, nil
}
