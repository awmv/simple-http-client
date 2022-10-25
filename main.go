package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

func getSecrets() (ISubscribeRequest, IGetTokenRequest) {
	err := godotenv.Load("local.env")
	if err != nil {
		log.Fatalf("cannot read env var %s", err)
	}

	return ISubscribeRequest{
			BaseURL: os.Getenv("SUB_BASE_URL"),
			Payload: ISubscribePayload{
				Offer:               os.Getenv("SUB_OFFER"),
				Account:             os.Getenv("SUB_ACCOUNT"),
				RebootAfterNextTrip: false,
			},
		}, IGetTokenRequest{
			BaseURL:   os.Getenv("AUTH_BASE_URL"),
			GrantType: os.Getenv("AUTH_GRANT_TYPE"),
			Username:  os.Getenv("AUTH_USERNAME"),
			Password:  os.Getenv("AUTH_PASSWORD"),
		}
}

type IResult map[string]interface{}

type IWorkerResult interface {
	Err() error
	Value() IResult
}

type ITokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	CreatedAt    int    `json:"created_at"`
}

type ISubscribeRequest struct {
	BaseURL string            `json:"base_url"`
	Payload ISubscribePayload `json:"payload"`
}

type ISubscribePayload struct {
	Offer               string `json:"offer"`
	Account             string `json:"account"`
	RebootAfterNextTrip bool   `json:"reboot_after_next_trip"`
}

type IGetTokenRequest struct {
	BaseURL   string `json:"base_url"`
	GrantType string `json:"grant_type"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

type IWorkerParams struct {
	Url     string
	Method  string
	Imei    string
	Payload ISubscribePayload
	Token   string
	Path    string
}

type IJsonResult struct {
	err   error
	value IResult
}

func (r IJsonResult) Err() error {
	return r.err
}

func (r IJsonResult) Value() IResult {
	return r.value
}

func getToken(cred IGetTokenRequest) (string, error) {

	payload, err := json.Marshal(cred)

	if err != nil {
		fmt.Println(err)
		return "", err
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/oauth/token", cred.BaseURL), strings.NewReader(string(payload)))

	if err != nil {
		return "", err
	}

	req.Header.Add("Content-Type", "application/json")

	res, err := client.Do(req)

	if err != nil {
		fmt.Println(err)
		return "", err
	}

	decoder := json.NewDecoder(res.Body)

	defer res.Body.Close()

	var t ITokenResponse
	if err = decoder.Decode(&t); err != nil {
		return "", err
	}

	return t.AccessToken, nil
}

func readFile(path string) ([]string, error) {
	file, err := os.Open(path)

	if err != nil {
		return nil, err
	}

	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	return lines, scanner.Err()
}

func appendToFile(path, content string) {
	file, err := os.OpenFile(path,
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println(err)
	}
	defer file.Close()
	if _, err := file.WriteString(fmt.Sprintf("%s\n", content)); err != nil {
		log.Println(err)
	}
}

func removeLine(path, content string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}

	tmpName := fmt.Sprintf("%s~tmp", path)
	out, err := os.Create(tmpName)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := scanner.Text(); line != content {
			out.WriteString(fmt.Sprintf("%s\n", line))
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	f.Close()
	out.Close()
	err = os.Rename(tmpName, path)

	return err
}

func doWork(client *http.Client, work <-chan IWorkerParams, results chan<- IWorkerResult, wg *sync.WaitGroup) {
	for params := range work {
		result, err := doRequest(client, params)
		if err != nil {
			results <- IJsonResult{err: err}
			continue
		}

		results <- IJsonResult{value: result}
	}
	wg.Done()
}

func doRequest(client *http.Client, params IWorkerParams) (IResult, error) {
	payload, err := json.Marshal(params.Payload)
	if err != nil {
		return nil, fmt.Errorf("encoding payload to json: %w", err)
	}

	req, err := http.NewRequest(params.Method, params.Url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("creating new request: %w", err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", params.Token))
	req.Header.Add("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		if os.IsTimeout(err) {
			appendToFile("./failed.txt", params.Imei)
		}
		return nil, fmt.Errorf("performing request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		appendToFile("./failed.txt", params.Imei)
		return nil, fmt.Errorf("unexpected response %s", res.Status)
	}

	// TODO: Refresh token on 401

	if err = removeLine(params.Path, params.Imei); err != nil {
		return nil, fmt.Errorf("removing line from text file: %w", err)
	}

	var result IResult
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding json response: %w", err)
	}

	return result, nil
}

func main() {

	args := os.Args[1:]
	if len(args) != 2 {
		fmt.Println("Provide arguments.")
		fmt.Println("Example ./binaryname 12 ./sourcefile.txt")
		return
	}

	assets, err := readFile(args[1])

	if err != nil {
		fmt.Println(err)
		return
	}

	wg := &sync.WaitGroup{}
	workers, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Println(err)
		return
	}

	work := make(chan IWorkerParams, len(assets))
	results := make(chan IWorkerResult, len(assets))

	client := &http.Client{Timeout: 5 * time.Second}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go doWork(client, work, results, wg)
	}

	subscribePayload, tokenPayload := getSecrets()

	token, err := getToken(tokenPayload)

	if err != nil {
		fmt.Println(err)
		return
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for _, imei := range assets {
		work <- IWorkerParams{
			Url:     fmt.Sprintf("%s/services/obdstack/v1/assets/%s/subscribe", subscribePayload.BaseURL, imei),
			Method:  "POST",
			Imei:    imei,
			Payload: subscribePayload.Payload,
			Token:   token,
			Path:    args[1],
		}
	}

	close(work)

	for result := range results {
		if result.Err() != nil {
			log.Println(result.Err())
		}
		fmt.Println(result.Value())
	}

	fmt.Println("Done")
}
