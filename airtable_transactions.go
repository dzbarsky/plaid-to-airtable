package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/brianloveswords/airtable"
	"github.com/plaid/plaid-go/plaid"
)

type TransactionFields struct {
	PlaidID      string
	// Used to dedupe, not for human consumption
	AccountID    string `json:"AccountIDDedupe"`
	AccountIDLink airtable.RecordLink `json:"AccountID"`
	Amount       float64
	Name         string
	MerchantName string
	Pending      bool
	DateTime     string
	PlaidCategory1   string
	PlaidCategory2   string
	PlaidCategory3   string
	Address string
	CategoryLookup airtable.RecordLink
	//CategoryLookup
}

type TransactionRecord struct {
	airtable.Record
	Fields   TransactionFields
	Typecast bool
}

func FetchAirtableTransactions() ([]TransactionRecord, error) {
	log.Println("Fetching airtable transactions...")
	client := airtable.Client{
		APIKey: os.Getenv("AIRTABLE_KEY"),
		BaseID: "appxCfKnRz94NZadj",
	}

	transactionsTable := client.Table("Transactions")

	var airtableTransactions []TransactionRecord
	err := transactionsTable.List(&airtableTransactions, &airtable.Options{})
	log.Println("Fetched airtable transactions")
	return airtableTransactions, err
}

func Sync(transactions []plaid.Transaction, airtableTransactions []TransactionRecord) error {
	client := airtable.Client{
		APIKey: os.Getenv("AIRTABLE_KEY"),
		BaseID: "appxCfKnRz94NZadj",
	}

	transactionsTable := client.Table("Transactions")

	plaidTransactions := make([]TransactionRecord, len(transactions))
	for i, t := range transactions {
		s := func(tags []string, n int) string {
			if n >= len(tags) {
				return ""
			}
			return tags[n]
		}
		address := t.Location.Address + " " + t.Location.City
		plaidTransactions[i] = TransactionRecord{Fields: TransactionFields{
			PlaidID:      t.ID,
			AccountID:    t.AccountID,
			AccountIDLink:    airtable.RecordLink{t.AccountID},
			Amount:       t.Amount,
			Name:         t.Name,
			MerchantName: t.MerchantName,
			Pending:      t.Pending,
			DateTime:     t.Date,
			PlaidCategory1:    s(t.Category, 0),
			PlaidCategory2:    s(t.Category, 1),
			PlaidCategory3:    s(t.Category, 2),
			Address: address,
		}, Typecast: true}
		plaidTransactions[i].ID = t.ID
	}

	plaidArranged := byAccountIDbyTransactionID(plaidTransactions)
	airtableArranged := byAccountIDbyTransactionID(airtableTransactions)

	for accountID, transactions := range plaidArranged {
		updates := updateAccount(transactions, airtableArranged[accountID])

		// Update is delete + create
		for _, t := range updates.ToDelete {
			err := transactionsTable.Delete(&t)
			if err != nil {
				return err
			}
		}

		for i, t := range updates.ToCreate {
			err := transactionsTable.Create(&t)
			if err != nil {
				return err
			}
			fmt.Printf("Created %d/%d transactions\n", i + 1, len(updates.ToCreate))
		}

		for i, t := range updates.ToUpdate {
			err := transactionsTable.Update(&t)
			if err != nil {
				return err
			}
			fmt.Printf("Updated %d/%d transactions\n", i + 1, len(updates.ToUpdate))
		}
	}

	return nil
}

func byAccountIDbyTransactionID(ts []TransactionRecord) map[string]map[string]TransactionRecord {
	ret := make(map[string]map[string]TransactionRecord)
	for _, t := range ts {
		byID, ok := ret[t.Fields.AccountID]
		if !ok {
			byID = make(map[string]TransactionRecord)
			ret[t.Fields.AccountID] = byID
		}
		byID[t.Fields.PlaidID] = t
	}
	return ret
}

type AccountUpdate struct {
	ToCreate []TransactionRecord
	ToDelete []TransactionRecord
	ToUpdate []TransactionRecord
}

func updateAccount(plaidTs, airtableTs map[string]TransactionRecord) AccountUpdate {
	var u AccountUpdate
	ids := make(map[string]struct{})
	for id, t := range plaidTs {
		ids[id] = struct{}{}
		existing, ok := airtableTs[id]
		if !ok {
			u.ToCreate = append(u.ToCreate, t)
		} else if existing.Fields.Pending != t.Fields.Pending ||
		        existing.Fields.Address != t.Fields.Address {
			t.ID = existing.ID
			u.ToUpdate = append(u.ToUpdate, t)
		}
	}

	cutoff := time.Now().AddDate(0, -1, 0)
	for id, t := range airtableTs {
		if _, ok := ids[id]; !ok {
			transactionTime, err := time.Parse("2006-01-02", t.Fields.DateTime)
			if err != nil {
				panic(err)
			}
			if transactionTime.After(cutoff) {
				fmt.Println("Deleting", t)
				u.ToDelete = append(u.ToDelete, t)
			}
		}
	}
	return u
}

func FixAT(airtableTransactions []TransactionRecord) error {
	client := airtable.Client{
		APIKey: os.Getenv("AIRTABLE_KEY"),
		BaseID: "appxCfKnRz94NZadj",
	}

	transactionsTable := client.Table("Transactions")
	_ = transactionsTable

	airtableArranged := make(map[string]map[string][]TransactionRecord)
	for _, t := range airtableTransactions {
		byAmount, ok := airtableArranged[t.Fields.DateTime]
		if !ok {
			byAmount = make(map[string][]TransactionRecord)
			airtableArranged[t.Fields.DateTime] = byAmount
		}
		key := fmt.Sprintf("%v%s", t.Fields.Amount, t.Fields.Name)
		byAmount[key] = append(byAmount[key], t)
	}

	for _, dateTs := range airtableArranged {
		for _, amountTs := range dateTs {
			if len(amountTs) == 1 {
				fmt.Println("ok")
			} else if len(amountTs) == 2 {
				fmt.Println(amountTs[0].Fields.CategoryLookup, amountTs[0].Fields.AccountID, amountTs[0].Fields.AccountIDLink, amountTs[0])
				fmt.Println(amountTs[1].Fields.CategoryLookup, amountTs[1].Fields.AccountID, amountTs[1].Fields.AccountIDLink, amountTs[1])
			} else {
				fmt.Println("fuck")
				for _, a := range amountTs {
					fmt.Println(a.Fields.AccountID, a.Fields.AccountIDLink, a)
				}
				//panic(fmt.Sprintf("fuck", len(amountTs)))
			}
		}
	}

	/*for accountID, transactions := range plaidArranged {
		updates := updateAccount(transactions, airtableArranged[accountID])

		// Update is delete + create
		for _, t := range updates.ToDelete {
			err := transactionsTable.Delete(&t)
			if err != nil {
				return err
			}
		}

		for i, t := range updates.ToCreate {
			err := transactionsTable.Create(&t)
			if err != nil {
				return err
			}
			fmt.Printf("Created %d/%d transactions\n", i + 1, len(updates.ToCreate))
		}

		for i, t := range updates.ToUpdate {
			err := transactionsTable.Update(&t)
			if err != nil {
				return err
			}
			fmt.Printf("Updated %d/%d transactions\n", i + 1, len(updates.ToUpdate))
		}
	}*/

	return nil
}


