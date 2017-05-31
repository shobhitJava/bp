package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hyperledger/fabric/core/chaincode/shim"
	"github.com/hyperledger/fabric/core/crypto/primitives"
)

var logger = shim.NewLogger("UFAChainCode")

//ALL_ELEMENENTS Key to refer the master list of UFA
const ALL_ELEMENENTS = "ALL_RECS"

//ALL_INVOICES key to refer the invoice master data
const ALL_INVOICES = "ALL_INVOICES"

//UFA_TRXN_PREFIX Key prefix for UFA transaction history
const UFA_TRXN_PREFIX = "UFA_TRXN_HISTORY_"

//UFA_INVOICE_PREFIX Key prefix for identifying Invoices assciated with a ufa
const UFA_INVOICE_PREFIX = "UFA_INVOICE_PREFIX_"

//UFAChainCode Chaincode default interface
type UFAChainCode struct {
}

//Retrives all the invoices for a ufa
func getInvoices(stub shim.ChaincodeStubInterface, args []string) ([]byte, error) {
	logger.Info("getInvoices called")
	ufanumber := args[0]
	//who:= args[1]
	outputBytes, _ := json.Marshal(getInvoicesForUFA(stub, ufanumber))
	logger.Info("getInvoices returning " + string(outputBytes))
	return outputBytes, nil
}

//Retrives an ivoice
func getInvoiceDetails(stub shim.ChaincodeStubInterface, args []string) ([]byte, error) {
	logger.Info("getInvoiceDetails called with UFA number: " + args[0])

	var outputRecord map[string]string
	invoiceNumber := args[0] //UFA ufanum
	//who :=args[1] //Role
	recBytes, _ := stub.GetState(invoiceNumber)
	json.Unmarshal(recBytes, &outputRecord)
	outputBytes, _ := json.Marshal(outputRecord)
	logger.Info("Returning records from getInvoiceDetails " + string(outputBytes))
	return outputBytes, nil
}

//Create new invoices
func createNewInvoices(stub shim.ChaincodeStubInterface, args []string) ([]byte, error) {
	logger.Info("createNewInvoice called")
	who := args[0]
	payload := args[1]
	//First validate the inputs
	validationMessag := validateInvoiceDetails(stub, args)
	if validationMessag == "" {
		var invoiceList []map[string]string
		json.Unmarshal([]byte(payload), &invoiceList)
		//Get the customer invoice
		custInvoice := invoiceList[0]
		//Get the vendor invoice
		vendInvoice := invoiceList[1]
		//Get the ufa details
		ufanumber := custInvoice["ufanumber"]
		var ufaDetails map[string]string
		//who :=args[1] //Role
		//Get the ufaDetails
		recBytes, _ := stub.GetState(ufanumber)
		json.Unmarshal(recBytes, &ufaDetails)
		//Calculate the updated invoide total
		raisedInvTotal := validateNumber(ufaDetails["raisedInvTotal"])
		invAmt := validateNumber(invoiceList[0]["invoiceAmt"])
		newRaisedTotal := raisedInvTotal + invAmt

		updaredRecPayload := "{ \"raisedInvTotal\" : \"" + strconv.FormatFloat(newRaisedTotal, 'f', -1, 64) + "\" } "
		//stub.PutState(,json.Marshal)
		bytesToStoreCustInvoice, _ := json.Marshal(custInvoice)
		bytesToStoreVendInvoice, _ := json.Marshal(vendInvoice)
		stub.PutState(custInvoice["invoiceNumber"], bytesToStoreCustInvoice)
		stub.PutState(vendInvoice["invoiceNumber"], bytesToStoreVendInvoice)
		//Append the invoice numbers to ufa details
		addInvoiceRecordsToUFA(stub, ufanumber, custInvoice["invoiceNumber"], vendInvoice["invoiceNumber"])
		//Update the master records
		updateInventoryMasterRecords(stub, custInvoice["invoiceNumber"], vendInvoice["invoiceNumber"])
		//Update the original ufa details
		var updateInput []string
		updateInput = make([]string, 3)
		updateInput[0] = ufanumber
		updateInput[1] = who
		updateInput[2] = updaredRecPayload
		logger.Info("createNewInvoice updating  the UFA details")
		return updateUFA(stub, updateInput)

	} else {
		return nil, errors.New("CreateNewInvoice Validation failure: " + validationMessag)
	}

}

//Validate Invoice
func validateInvoiceDetails(stub shim.ChaincodeStubInterface, args []string) string {

	logger.Info("validateInvoice called")
	var validationMessage bytes.Buffer
	//who := args[0]
	payload := args[1]
	//I am assuming the payload will be an array of Invoices
	//Once for cusotmer and another for vendor
	//Checking only one would be sufficient from the amount perspective
	var invoiceList []map[string]string
	json.Unmarshal([]byte(payload), &invoiceList)
	if len(invoiceList) < 2 {
		validationMessage.WriteString("\nInvoice is missing for Customer or Vendor")
	} else {
		//Get the UFA number
		ufanumber := invoiceList[0]["ufanumber"]
		var ufaDetails map[string]string
		//who :=args[1] //Role
		//Get the ufaDetails
		recBytes, err := stub.GetState(ufanumber)
		if err != nil || recBytes == nil {
			validationMessage.WriteString("\nInvalid UFA provided")
		} else {
			json.Unmarshal(recBytes, &ufaDetails)
			tolerence := validateNumber(ufaDetails["chargTolrence"])
			netCharge := validateNumber(ufaDetails["netCharge"])

			raisedInvTotal := validateNumber(ufaDetails["raisedInvTotal"])
			//Calculate the max charge
			maxCharge := netCharge + netCharge*tolerence/100.0
			//We are assumming 2 invoices have the same amount in it
			invAmt1 := validateNumber(invoiceList[0]["invoiceAmt"])
			invAmt2 := validateNumber(invoiceList[1]["invoiceAmt"])
			billingPeriod := invoiceList[0]["billingPeriod"]
			if checkInvoicesRaised(stub, ufanumber, billingPeriod) {
				validationMessage.WriteString("\nInvoices are already raised for " + billingPeriod)
			} else if invAmt1 != invAmt2 {
				validationMessage.WriteString("\nCustomer and Vendor Invoice Amounts are not same")
			} else if maxCharge < (invAmt1 + raisedInvTotal) {
				validationMessage.WriteString("\nTotal invoice amount exceeded")
			}
		} // Invalid UFA number
	} // End of length of invoics
	finalMessage := validationMessage.String()
	logger.Info("validateInvoice Validation message generated :" + finalMessage)
	return finalMessage
}

//Checking if invoice is already raised or not
func checkInvoicesRaised(stub shim.ChaincodeStubInterface, ufaNumber string, billingPeriod string) bool {

	var isAvailable = false
	logger.Info("checkInvoicesRaised started for :" + ufaNumber + " : Billing month " + billingPeriod)
	allInvoices := getInvoicesForUFA(stub, ufaNumber)
	if len(allInvoices) > 0 {
		for _, invoiceDetails := range allInvoices {
			logger.Info("checkInvoicesRaised checking for invoice number :" + invoiceDetails["invoiceNumber"])
			if invoiceDetails["billingPeriod"] == billingPeriod {
				isAvailable = true
				break
			}
		}
	}
	return isAvailable
}

//Returns all the invoices raised for an UFA
func getInvoicesForUFA(stub shim.ChaincodeStubInterface, ufanumber string) []map[string]string {
	logger.Info("getInvoicesForUFA called")
	var outputRecords []map[string]string
	outputRecords = make([]map[string]string, 0)

	recordsList, err := getAllInvloiceList(stub, ufanumber)
	if err == nil {
		for _, invoiceNumber := range recordsList {
			logger.Info("getInvoicesForUFA: Processing record " + ufanumber)
			recBytes, _ := stub.GetState(invoiceNumber)
			var record map[string]string
			json.Unmarshal(recBytes, &record)
			outputRecords = append(outputRecords, record)
		}

	}

	logger.Info("Returning records from getInvoicesForUFA ")
	return outputRecords
}

//Retrieve all the invoice list
func getAllInvloiceList(stub shim.ChaincodeStubInterface, ufanumber string) ([]string, error) {
	var recordList []string
	recBytes, _ := stub.GetState(UFA_INVOICE_PREFIX + ufanumber)

	err := json.Unmarshal(recBytes, &recordList)
	if err != nil {
		return nil, errors.New("Failed to unmarshal getAllInvloiceList ")
	}

	return recordList, nil
}

//Retrieve all the invoice list
func getAllInvloiceFromMasterList(stub shim.ChaincodeStubInterface) ([]string, error) {
	var recordList []string
	recBytes, _ := stub.GetState(ALL_INVOICES)

	err := json.Unmarshal(recBytes, &recordList)
	if err != nil {
		return nil, errors.New("Failed to unmarshal getAllInvloiceFromMasterList ")
	}

	return recordList, nil
}

//Append the invoice number to the UFA
func addInvoiceRecordsToUFA(stub shim.ChaincodeStubInterface, ufanumber string, custInvoiceNum string, vendInvoiceNum string) error {
	logger.Info("Adding invoice numbers to UFA" + ufanumber)
	var recordList []string
	recBytes, _ := stub.GetState(UFA_INVOICE_PREFIX + ufanumber)

	err := json.Unmarshal(recBytes, &recordList)
	if err != nil || recBytes == nil {
		recordList = make([]string, 0)
	}
	recordList = append(recordList, custInvoiceNum)
	recordList = append(recordList, vendInvoiceNum)

	bytesToStore, _ := json.Marshal(recordList)
	logger.Info("After addition" + string(bytesToStore))
	stub.PutState(UFA_INVOICE_PREFIX+ufanumber, bytesToStore)
	logger.Info("Adding invoice numbers to UFA :Done ")
	return nil
}

//Append a new UFA numbetr to the master list
func updateMasterRecords(stub shim.ChaincodeStubInterface, ufaNumber string) error {
	var recordList []string
	recBytes, _ := stub.GetState(ALL_ELEMENENTS)

	err := json.Unmarshal(recBytes, &recordList)
	if err != nil {
		return errors.New("Failed to unmarshal updateMasterReords ")
	}
	recordList = append(recordList, ufaNumber)
	bytesToStore, _ := json.Marshal(recordList)
	logger.Info("After addition" + string(bytesToStore))
	stub.PutState(ALL_ELEMENENTS, bytesToStore)
	return nil
}

//Append a new invoices to the master list
func updateInventoryMasterRecords(stub shim.ChaincodeStubInterface, custInvoice string, vendInvoice string) error {
	var recordList []string
	recBytes, _ := stub.GetState(ALL_INVOICES)

	err := json.Unmarshal(recBytes, &recordList)
	if err != nil {
		return errors.New("Failed to unmarshal updateInventoryMasterRecords ")
	}
	recordList = append(recordList, custInvoice)
	recordList = append(recordList, vendInvoice)

	bytesToStore, _ := json.Marshal(recordList)
	logger.Info("After addition" + string(bytesToStore))
	stub.PutState(ALL_INVOICES, bytesToStore)
	return nil
}

//Append to UFA transaction history
func appendUFATransactionHistory(stub shim.ChaincodeStubInterface, ufanumber string, payload string) error {
	var recordList []string

	logger.Info("Appending to transaction history " + ufanumber)
	recBytes, _ := stub.GetState(UFA_TRXN_PREFIX + ufanumber)

	if recBytes == nil {
		logger.Info("Updating the transaction history for the first time")
		recordList = make([]string, 0)
	} else {
		err := json.Unmarshal(recBytes, &recordList)
		if err != nil {
			return errors.New("Failed to unmarshal appendUFATransactionHistory ")
		}
	}
	recordList = append(recordList, payload)
	bytesToStore, _ := json.Marshal(recordList)
	logger.Info("After updating the transaction history" + string(bytesToStore))
	stub.PutState(UFA_TRXN_PREFIX+ufanumber, bytesToStore)
	logger.Info("Appending to transaction history " + ufanumber + " Done!!")
	return nil
}

//Returns all the UFA Numbers stored
func getAllRecordsList(stub shim.ChaincodeStubInterface) ([]string, error) {
	var recordList []string
	recBytes, _ := stub.GetState(ALL_ELEMENENTS)

	err := json.Unmarshal(recBytes, &recordList)
	if err != nil {
		return nil, errors.New("Failed to unmarshal getAllRecordsList ")
	}

	return recordList, nil
}

// Creating a new Upfront agreement
func createUFA(stub shim.ChaincodeStubInterface, args []string) ([]byte, error) {
	logger.Info("createUFA called")

	ufanumber := args[0]
	who := args[1]
	payload := args[2]
	//If there is no error messages then create the UFA
	valMsg := validateNewUFA(who, payload)
	if valMsg == "" {
		stub.PutState(ufanumber, []byte(payload))

		updateMasterRecords(stub, ufanumber)
		appendUFATransactionHistory(stub, ufanumber, payload)
		logger.Info("Created the UFA after successful validation : " + payload)
	} else {
		return nil, errors.New("Validation failure: " + valMsg)
	}
	return nil, nil
}

//Validate a new UFA
func validateNewUFA(who string, payload string) string {

	//As of now I am checking if who is of proper role
	var validationMessage bytes.Buffer
	var ufaDetails map[string]string

	logger.Info("validateNewUFA")
	if who == "SELLER" || who == "BUYER" {
		json.Unmarshal([]byte(payload), &ufaDetails)
		//Now check individual fields
		netChargeStr := ufaDetails["netCharge"]
		tolerenceStr := ufaDetails["chargTolrence"]
		netCharge := validateNumber(netChargeStr)
		if netCharge <= 0.0 {
			validationMessage.WriteString("\nInvalid net charge")
		}
		tolerence := validateNumber(tolerenceStr)
		if tolerence < 0.0 || tolerence > 10.0 {
			validationMessage.WriteString("\nTolerence is out of range. Should be between 0 and 10")
		}

	} else {
		validationMessage.WriteString("\nUser is not authorized to create a UFA")
	}
	logger.Info("Validation messagge " + validationMessage.String())
	return validationMessage.String()
}

//Validate a input string as number or not
func validateNumber(str string) float64 {
	if netCharge, err := strconv.ParseFloat(str, 64); err == nil {
		return netCharge
	}
	return float64(-1.0)
}

//Update the existing record with the mofied key value pair
func updateRecord(existingRecord map[string]string, fieldsToUpdate map[string]string) (string, error) {
	for key, value := range fieldsToUpdate {

		existingRecord[key] = value
	}
	outputMapBytes, _ := json.Marshal(existingRecord)
	logger.Info("updateRecord: Final json after update " + string(outputMapBytes))
	return string(outputMapBytes), nil
}

// Update and existing UFA record
func updateUFA(stub shim.ChaincodeStubInterface, args []string) ([]byte, error) {
	var existingRecMap map[string]string
	var updatedFields map[string]string

	logger.Info("updateUFA called ")

	ufanumber := args[0]
	//TODO: Update the validation here
	//who := args[1]
	payload := args[2]
	logger.Info("updateUFA payload passed " + payload)

	//who :=args[2]
	recBytes, _ := stub.GetState(ufanumber)

	json.Unmarshal(recBytes, &existingRecMap)
	json.Unmarshal([]byte(payload), &updatedFields)
	updatedReord, _ := updateRecord(existingRecMap, updatedFields)
	//Store the records
	stub.PutState(ufanumber, []byte(updatedReord))
	appendUFATransactionHistory(stub, ufanumber, payload)
	return nil, nil
}

//Returns all the UFAs created so far
func getAllUFA(stub shim.ChaincodeStubInterface, who string) ([]byte, error) {
	logger.Info("getAllUFA called")

	recordsList, err := getAllRecordsList(stub)
	if err != nil {
		return nil, errors.New("Unable to get all the records ")
	}
	var outputRecords []map[string]string
	outputRecords = make([]map[string]string, 0)
	for _, ufanumber := range recordsList {
		logger.Info("getAllUFA: Processing record " + ufanumber)
		recBytes, _ := stub.GetState(ufanumber)
		var record map[string]string
		json.Unmarshal(recBytes, &record)
		outputRecords = append(outputRecords, record)
	}
	outputBytes, _ := json.Marshal(outputRecords)
	logger.Info("Returning records from getAllUFA " + string(outputBytes))
	return outputBytes, nil
}

//Returns all the Invoice created so far for the interest parties
func getAllInvoicesForUsr(stub shim.ChaincodeStubInterface, args []string) ([]byte, error) {
	logger.Info("getAllInvoicesForUsr called")
	who := args[0]

	recordsList, err := getAllInvloiceFromMasterList(stub)
	if err != nil {
		return nil, errors.New("Unable to get all the inventory records ")
	}
	var outputRecords []map[string]string
	outputRecords = make([]map[string]string, 0)
	for _, invoiceNumber := range recordsList {
		logger.Info("getAllInvoicesForUsr: Processing inventory record " + invoiceNumber)
		recBytes, _ := stub.GetState(invoiceNumber)
		var record map[string]string
		json.Unmarshal(recBytes, &record)
		if record["approverBy"] == who || record["raisedBy"] == who {
			outputRecords = append(outputRecords, record)
		}
	}
	outputBytes, _ := json.Marshal(outputRecords)
	logger.Info("Returning records from getAllInvoicesForUsr " + string(outputBytes))
	return outputBytes, nil
}

//Get a single ufa
func getUFADetails(stub shim.ChaincodeStubInterface, args []string) ([]byte, error) {
	logger.Info("getUFADetails called with UFA number: " + args[0])

	var outputRecord map[string]string
	ufanumber := args[0] //UFA ufanum
	//who :=args[1] //Role
	recBytes, _ := stub.GetState(ufanumber)
	json.Unmarshal(recBytes, &outputRecord)
	outputBytes, _ := json.Marshal(outputRecord)
	logger.Info("Returning records from getUFADetails " + string(outputBytes))
	return outputBytes, nil
}

func probe() []byte {
	ts := time.Now().Format(time.UnixDate)
	output := "{\"status\":\"Success\",\"ts\" : \"" + ts + "\" }"
	return []byte(output)
}

//Validate the new UFA
func validateNewUFAData(args []string) []byte {
	var output string
	msg := validateNewUFA(args[0], args[1])

	if msg == "" {
		output = "{\"validation\":\"Success\",\"msg\" : \"\" }"
	} else {
		output = "{\"validation\":\"Failure\",\"msg\" : \"" + msg + "\" }"
	}
	return []byte(output)
}

//Validate the new Invoice created
func validateNewInvoideData(stub shim.ChaincodeStubInterface, args []string) []byte {
	var output string
	msg := validateInvoiceDetails(stub, args)

	if msg == "" {
		output = "{\"validation\":\"Success\",\"msg\" : \"\" }"
	} else {
		output = "{\"validation\":\"Failure\",\"msg\" : \"" + msg + "\" }"
	}
	return []byte(output)
}

// Init initializes the smart contracts
func (t *UFAChainCode) Init(stub shim.ChaincodeStubInterface, function string, args []string) ([]byte, error) {
	logger.Info("Init called")
	//Place an empty arry
	stub.PutState(ALL_ELEMENENTS, []byte("[]"))
	stub.PutState(ALL_INVOICES, []byte("[]"))
	return nil, nil
}

// Invoke entry point
func (t *UFAChainCode) Invoke(stub shim.ChaincodeStubInterface, function string, args []string) ([]byte, error) {
	logger.Info("Invoke called")

	if function == "createUFA" {
		createUFA(stub, args)
	} else if function == "updateUFA" {
		updateUFA(stub, args)
	} else if function == "createNewInvoices" {
		createNewInvoices(stub, args)
	}

	return nil, nil
}

// Query the rcords form the  smart contracts
func (t *UFAChainCode) Query(stub shim.ChaincodeStubInterface, function string, args []string) ([]byte, error) {
	logger.Info("Query called")
	if function == "getAllUFA" {
		return getAllUFA(stub, args[0])
	} else if function == "getUFADetails" {
		return getUFADetails(stub, args)
	} else if function == "probe" {
		return probe(), nil
	} else if function == "validateNewUFA" {
		return validateNewUFAData(args), nil
	} else if function == "validateNewInvoideData" {
		return validateNewInvoideData(stub, args), nil
	} else if function == "getInvoices" {
		return getInvoices(stub, args)
	} else if function == "getInvoiceDetails" {
		return getInvoiceDetails(stub, args)
	} else if function == "getAllInvoicesForUsr" {
		return getAllInvoicesForUsr(stub, args)
	}
	return nil, nil
}

//Main method
func main() {
	logger.SetLevel(shim.LogInfo)
	primitives.SetSecurityLevel("SHA3", 256)
	err := shim.Start(new(UFAChainCode))
	if err != nil {
		fmt.Printf("Error starting UFAChainCode: %s", err)
	}
}
