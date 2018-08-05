package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/getsentry/raven-go"
)

var (
	// MonzoAccountID Monzo account ID to received transactions from
	MonzoAccountID = os.Getenv("MONZO_ACCOUNT_ID")
	// YnabAccountID YNAB account ID to add transactions to
	YnabAccountID = os.Getenv("YNAB_ACCOUNT_ID")
	// YnabAPIKey YNAB API key
	YnabAPIKey = os.Getenv("YNAB_API_KEY")
	// YnabBaseURL YNAB API base URL
	YnabBaseURL = os.Getenv("YNAB_BASE_URL")
	// YnabBudgetID YNAB budget ID to add transactions to
	YnabBudgetID = os.Getenv("YNAB_BUDGET_ID")
)

// MonzoTransaction JSON
type MonzoTransaction struct {
	TransactionType string `json:"type"`
	Data            struct {
		AccountID    string  `json:"account_id"`
		Amount       float64 `json:"amount"`
		Created      string  `json:"created"`
		Description  string  `json:"description"`
		Counterparty struct {
			Name string `json:"name"`
		} `json:"counterparty"`
		TransactionID string `json:"id"`
		Merchant      struct {
			Name string `json:"name"`
		} `json:"merchant"`
	} `json:"data"`
}

// YNABTransactionWrapper JSON
type YNABTransactionWrapper struct {
	YNABTransaction `json:"transaction"`
}

// YNABTransaction JSON
type YNABTransaction struct {
	AccountID string  `json:"account_id"`
	Date      string  `json:"date"`
	Amount    float64 `json:"amount"`
	Payee     string  `json:"payee_name"`
	Cleared   string  `json:"cleared"`
	ImportID  string  `json:"import_id"`
}

func init() {
	raven.SetDSN("https://9d6201646c5444ef9564485e489f140f:9c896af3bfcc49e3a442def157867e2b@sentry.io/1256118")
	envvars := []string{MonzoAccountID, YnabAccountID, YnabAPIKey, YnabBaseURL, YnabBudgetID}
	log.Println("Environmental vars:")
	for _, x := range envvars {
		log.Println(x)
		if x == "" {
			raven.CaptureErrorAndWait(errors.New("Could not load environmental vars"), nil)
			panic(errors.New("Could not load environmental vars"))
		}
	}
}

func handler(request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	log.Println("Lambda request", request.RequestContext.RequestID)
	// Decode JSON
	log.Println("Request body", request.Body)
	raw := MonzoTransaction{}
	if err := json.Unmarshal([]byte(request.Body), &raw); err != nil {
		raven.CaptureErrorAndWait(err, map[string]string{"request": request.Body})
		log.Panic(err)
	}
	log.Println("Monzo transaction", raw)
	// Check that the transaction from Monzo matches the accountID we want
	if raw.Data.AccountID == MonzoAccountID {
		log.Printf("Transaction account (%s) matches Monzo account(%s)", raw.Data.AccountID, MonzoAccountID)
		var payee string
		if len(raw.Data.Merchant.Name) > 0 {
			payee = raw.Data.Merchant.Name
		} else if len(raw.Data.Counterparty.Name) > 0 {
			payee = raw.Data.Counterparty.Name
		} else if len(raw.Data.Description) > 0 {
			payee = raw.Data.Description
		} else {
			raven.CaptureErrorAndWait(errors.New("No payee data found"), map[string]string{"request": request.Body})
			return events.APIGatewayProxyResponse{
				Body:       string("No payee data found"),
				StatusCode: 500,
			}, errors.New("No payee data found")
		}
		// Build YNAB transaction
		trans := &YNABTransactionWrapper{
			YNABTransaction: YNABTransaction{
				AccountID: YnabAccountID,
				Date:      raw.Data.Created,
				Amount:    raw.Data.Amount * 10,
				Payee:     payee,
				Cleared:   "cleared",
				ImportID:  raw.Data.TransactionID,
			},
		}
		log.Println("YNAB transaction", trans)
		response, err := json.Marshal(trans)
		if err != nil {
			raven.CaptureErrorAndWait(err, map[string]string{"request": request.Body})
			return events.APIGatewayProxyResponse{
				StatusCode: 500,
			}, err
		}

		// Send transaction
		url := fmt.Sprintf(YnabBaseURL, YnabBudgetID)
		log.Println("YNAB POST URL", url)
		req, err := http.NewRequest("POST", url, bytes.NewBuffer(response))
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", YnabAPIKey))
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{}
		client.Timeout = time.Second * 5 // Set timeout for request
		resp, err := client.Do(req)
		if err != nil {
			raven.CaptureErrorAndWait(err, map[string]string{"request": request.Body})
			return events.APIGatewayProxyResponse{
				StatusCode: 500,
			}, err
		}
		defer resp.Body.Close()

		body, _ := ioutil.ReadAll(resp.Body)
		post := fmt.Sprintf("POST status: %s - %s", resp.Status, body)
		log.Println(post)
		return events.APIGatewayProxyResponse{
			Body:       string(post),
			StatusCode: 200,
		}, nil
	} else {
		// Invalid Monzo account
		raven.CaptureErrorAndWait(errors.New("Invalid Monzo account ID"), map[string]string{"request": request.Body})
		return events.APIGatewayProxyResponse{
			Body:       string("Invalid Monzo account ID"),
			StatusCode: 500,
		}, errors.New("Invalid Monzo account ID")
	}
}

func main() {
	raven.CapturePanic(func() {
		lambda.Start(handler)
	}, nil)
}
