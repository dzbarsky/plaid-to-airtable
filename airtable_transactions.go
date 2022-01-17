package main

import (
	"fmt"
	"github.com/brianloveswords/airtable"
	"github.com/plaid/plaid-go/plaid"
	"os"
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
}

type TransactionRecord struct {
	airtable.Record
	Fields   TransactionFields
	Typecast bool
}

func Sync(transactions []plaid.Transaction) error {
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
		}, Typecast: true}
		plaidTransactions[i].ID = t.ID
	}
	plaidArranged := byAccountIDbyTransactionID(plaidTransactions)

	var airtableTransactions []TransactionRecord
	err := transactionsTable.List(&airtableTransactions, &airtable.Options{})
	if err != nil {
		return err
	}
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
}

func updateAccount(plaidTs, airtableTs map[string]TransactionRecord) AccountUpdate {
	var u AccountUpdate
	ids := make(map[string]struct{})
	for id, t := range plaidTs {
		ids[id] = struct{}{}
		existing, ok := airtableTs[id]
		if !ok {
			u.ToCreate = append(u.ToCreate, t)
		} else if existing.Fields.Pending != t.Fields.Pending {
			u.ToCreate = append(u.ToCreate, t)
			// Need to use the linked record ID to make deletion work correctly.
			t.ID = existing.ID
			u.ToDelete = append(u.ToDelete, t)
		}
	}

	for id, t := range airtableTs {
		if _, ok := ids[id]; !ok {
			u.ToDelete = append(u.ToDelete, t)
		}
	}
	return u
}
