/**
 * GO_LRS
 * https://github.com/MonkoftheFunk/GO_LRS
 */

package main

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/nu7hatch/gouuid"
	"io"
	"io/ioutil"
	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func main() {
	r := mux.NewRouter()

	r.Path("/statements/").Methods("POST").HandlerFunc(PostStatement)
	r.Path("/statements/").Methods("PUT").HandlerFunc(PutStatement)
	r.Path("/statements/").Methods("GET").HandlerFunc(GetStatement)
	r.Path("/statements/").Methods("DELETE").HandlerFunc(DelStatement)
	http.Handle("/", r)
	http.ListenAndServe(":8080", nil)
}

//todo make index
func dbSession() *mgo.Session {
	session, err := mgo.Dial("localhost")
	if err != nil {
		panic(err)
	}
	return session
}

//todo perhaps convert certain objects to array of single object for
//ease of mapping
func isRootArray(r io.Reader) (bool, []byte, error) {
	body, err := ioutil.ReadAll(r)
	if err != nil {
		return false, body, err
	}

	isArray := false
	for _, c := range body {
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			continue
		}
		isArray = c == '['
		break
	}

	return isArray, body, err
}

func convertStatementElementsObjToArray(tempStatement map[string]interface{}) (Statement, error) {
	var statement Statement

	// todo go through each possible single object elements that are supposed to be arrays
	if tempStatement["context"] != nil &&
		tempStatement["context"].(map[string]interface{})["contextActivities"] != nil {
		ca := convertContextActivitiesToArray(tempStatement["context"].(map[string]interface{})["contextActivities"].(map[string]interface{}))
		tempStatement["context"].(map[string]interface{})["contextActivities"] = ca

	}

	if tempStatement["object"] != nil &&
		tempStatement["object"].(map[string]interface{})["context"] != nil &&
		tempStatement["object"].(map[string]interface{})["context"].(map[string]interface{})["contextActivities"] != nil {
		ca := convertContextActivitiesToArray(tempStatement["object"].(map[string]interface{})["context"].(map[string]interface{})["contextActivities"].(map[string]interface{}))
		tempStatement["object"].(map[string]interface{})["context"].(map[string]interface{})["contextActivities"] = ca
	}

	// turn in back to a string
	b, err := json.Marshal(tempStatement)
	if err != nil {
		return statement, err
	}

	// then turn that string into the struct
	err = json.Unmarshal(b, &statement)
	if err != nil {
		return statement, err
	}
	return statement, nil
}

func convertContextActivitiesToArray(tempCA map[string]interface{}) map[string]interface{} {
	if tempCA != nil {
		// check if the element is a map, then change to array
		if tempCA["parent"] != nil && reflect.TypeOf(tempCA["parent"]).Kind() == reflect.Map {
			var a []interface{}
			tempCA["parent"] = append(a, tempCA["parent"])
		}

		if tempCA["grouping"] != nil && reflect.TypeOf(tempCA["grouping"]).Kind() == reflect.Map {
			var a []interface{}
			tempCA["grouping"] = append(a, tempCA["grouping"])
		}

		if tempCA["category"] != nil && reflect.TypeOf(tempCA["category"]).Kind() == reflect.Map {
			var a []interface{}
			tempCA["category"] = append(a, tempCA["category"])
		}

		if tempCA["other"] != nil && reflect.TypeOf(tempCA["other"]).Kind() == reflect.Map {
			var a []interface{}
			tempCA["other"] = append(a, tempCA["other"])
		}

	}
	return tempCA
}

func PreProcessStatement(r io.Reader) (Statement, error) {
	// read whole stream
	var statement Statement
	body, err := ioutil.ReadAll(r)
	if err != nil {
		return statement, err
	}

	// create temporary map for processing
	var tempStatement map[string]interface{}
	err = json.Unmarshal(body, &tempStatement)
	if err != nil {
		return statement, err
	}

	// process temporary map to statement
	statement, err = convertStatementElementsObjToArray(tempStatement)
	return statement, err
}

func PreProcessStatements(r io.Reader) ([]Statement, error) {
	// determin if array of statements or just single statement
	isArray, body, err := isRootArray(r)
	if err != nil {
		return nil, err
	}

	var statement Statement
	var statements []Statement
	if isArray {
		// create temporary array map for processing
		var tempStatements []map[string]interface{}
		err = json.Unmarshal(body, &tempStatements)
		if err != nil {
			return statements, err
		}

		// loop through each statement to convert
		for _, s := range tempStatements {
			// process temporary map to statement
			statement, err = convertStatementElementsObjToArray(s)
			if err != nil {
				return statements, err
			}
			// create statement array used for Post
			statements = append(statements, statement)
		}
	} else {
		// create temporary map for processing
		var tempStatement map[string]interface{}
		err = json.Unmarshal(body, &tempStatement)
		if err != nil {
			return statements, err
		}
		// process temporary map to statement
		statement, err = convertStatementElementsObjToArray(tempStatement)
		if err != nil {
			return statements, err
		}
		// create statement array used for Post
		statements = append(statements, statement)
	}
	return statements, nil
}

func PostStatement(w http.ResponseWriter, r *http.Request) {

	// check if post is being used as a put
	statementId := r.FormValue("statementId")
	if statementId != "" {
		PutStatement(w, r)
		return
	}

	w.Header().Add("x-experience-api-version", "1.0")
	defer r.Body.Close()

	statements, err := PreProcessStatements(r.Body)
	if err != nil {
		// fmt.Fprint(w, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// connect to db
	session := dbSession()
	defer session.Close()
	statementsC := session.DB("LRS").C("statements")
	index := mgo.Index{
		Key:        []string{"id"},
		Unique:     true,
		DropDups:   true,
		Background: true,
		Sparse:     true,
	}
	err = statementsC.EnsureIndex(index)
	if err != nil {
		//fmt.Fprint(w, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// check if trying to replace object with same id
	status := checkIdConflictBatch(w, statementsC, statements)
	if status != 0 {
		w.WriteHeader(status)
		// fmt.Fprint(w, status)
		return
	}

	// go through each statement to validate and store
	var sids []string
	for _, s := range statements {

		sid, status := s.Validate()
		if status != 0 {
			w.WriteHeader(http.StatusBadRequest)
			// fmt.Fprint(w, err)
			return
		}

		// output new ids
		sids = append(sids, sid)

		//check if voided
		status = checkSpecialActionVerbs(w, statementsC, s)
		if status != 0 {
			w.WriteHeader(status)
			// fmt.Fprint(w, status)
			return
		}

		// save to db
		statementsC.Insert(s)
	}

	// return 200 with statement id(s), same order index
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	enc.Encode(sids)
	return
}

func PutStatement(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("x-experience-api-version", "1.0")
	defer r.Body.Close()

	// verify statementId passed in
	statementId := r.FormValue("statementId")
	if statementId == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	s, err := PreProcessStatement(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "decode error")
		fmt.Fprint(w, err)
		return
	}

	// connect to db
	session := dbSession()
	defer session.Close()
	statementsC := session.DB("LRS").C("statements")
	index := mgo.Index{
		Key:        []string{"id"},
		Unique:     true,
		DropDups:   true,
		Background: true,
		Sparse:     true,
	}
	err = statementsC.EnsureIndex(index)
	if err != nil {
		//fmt.Fprint(w, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// check if trying to replace object with same id
	s.Id = statementId
	status := checkIdConflict(w, statementsC, s)
	if status != 0 {
		w.WriteHeader(status)
		//fmt.Fprint(w, "confilct "+string(status))
		return
	}

	_, status = s.Validate()
	if status != 0 {
		w.WriteHeader(http.StatusBadRequest)
		// fmt.Fprint(w, "Validate "+string(status))
		return
	}

	//check if voided/unvoided
	status = checkSpecialActionVerbs(w, statementsC, s)
	if status != 0 {
		w.WriteHeader(status)
		// fmt.Fprint(w, "Void "+string(status))
		return
	}

	// save to db
	statementsC.Insert(s)

	// no response back
	w.WriteHeader(http.StatusNoContent)
	return
}

func checkSpecialActionVerbs(w http.ResponseWriter, statementsC *mgo.Collection, statement Statement) int {

	//voiding
	if statement.Verb.Id == "http://adlnet.gov/expapi/verbs/voided" {

		// check has statementId to void
		if statement.Object.ObjectType != "StatementRef" {
			// fmt.Fprint(w, "StatementRef")
			return http.StatusBadRequest
		}

		if statement.Object.Id == "" {
			// fmt.Fprint(w, "Object.Id")
			return http.StatusBadRequest
		}

		// find if statement to be voided exists
		var result Statement
		err := statementsC.Find(bson.M{"id": statement.Object.Id}).One(&result)
		if err != nil {
			// fmt.Fprint(w, "not found refrence"+statement.Object.Id)
			// fmt.Fprint(w, err)
			return http.StatusBadRequest
		}

		// if statement hasn't been voided do so
		if result.Void != true {
			err = statementsC.Update(bson.M{"id": result.Id}, bson.M{"$set": bson.M{"void": true}})
			if err != nil {
				// fmt.Fprint(w, "cant update")
				//fmt.Fprint(w, err)
				return http.StatusBadRequest
			}
		}
	} else if statement.Object.ObjectType == "StatementRef" { //check if statement's ref exists (might be moved to validate)
		if statement.Object.Id == "" {
			//fmt.Fprint(w, "Object.Id")
			return http.StatusBadRequest
		}

		// find if statement to be voided exists
		var result Statement
		err := statementsC.Find(bson.M{"id": statement.Object.Id}).One(&result)
		if err != nil {
			//fmt.Fprint(w, "not found refrence")
			return http.StatusBadRequest
		}
	}
	return 0
}

func checkIdConflictBatch(w http.ResponseWriter, statementsC *mgo.Collection, statements []Statement) int {

	// build array of IDs to query if statement(s) exist
	var IDs []string
	Lkup := make(map[string]Statement)

	for _, s := range statements {
		if s.Id != "" {
			IDs = append(IDs, s.Id)
			Lkup[s.Id] = s
		}
	}

	// if so then check if they are the same object so as not to throw conflict
	if IDs != nil {
		// find any statements with those ids
		var result []Statement
		err := statementsC.Find(bson.M{"id": bson.M{"$in": IDs}}).All(&result)

		if err != nil {
			return 0
		}

		for _, s := range result {
			// can't compare structs with arrays/maps
			// kludge the result has "stored" so add field for comparison
			s.Stored = Lkup[s.Id].Stored

			rj, _ := json.Marshal(Lkup[s.Id])
			sj, _ := json.Marshal(s)

			if string(rj) != string(sj) {
				return http.StatusConflict
			}
		}
	}
	return 0
}

func checkIdConflict(w http.ResponseWriter, statementsC *mgo.Collection, statement Statement) int {

	if statement.Id != "" {
		// find any statements with this id
		var result Statement
		err := statementsC.Find(bson.M{"id": statement.Id}).One(&result)
		if err != nil {
			//fmt.Fprint(w, err)
			return 0
		}

		// kludge the result has "stored" so add field for comparison
		statement.Stored = result.Stored

		// can't compare structs with arrays/maps
		rj, _ := json.Marshal(result)
		sj, _ := json.Marshal(statement)

		if string(rj) != string(sj) {
			return http.StatusConflict
		} else {
			//same no need to insert
			return http.StatusNoContent
		}
	}
	return 0
}

func DelStatement(w http.ResponseWriter, r *http.Request) {
	// todo serious auth checks
	statementId := r.FormValue("statementId")
	if statementId == "" {
		//fmt.Fprint(w, "no id")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// connect to db
	session := dbSession()
	defer session.Close()
	statementsC := session.DB("LRS").C("statements")

	// del statement
	err := statementsC.Remove(bson.M{"id": statementId})
	if err != nil {
		// fmt.Fprint(w, err)
		w.WriteHeader(http.StatusNotFound)
		return
	}
}

func GetStatement(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("x-experience-api-version", "1.0")

	// --validate
	// check if format ['exact', 'canonical', 'ids'] default exact
	formats_allowed := map[string]bool{"exact": true, "canonical": true, "ids": true}
	format := r.FormValue("format")
	if format == "" || formats_allowed[format] == false {
		format = "exact"
	}

	// check if	contain both statementId and voidedStatementId parameters then 400
	statementId := r.FormValue("statementId")
	voidedStatementId := r.FormValue("voidedStatementId")
	/*if statementId == "" && voidedStatementId == "" {
		//fmt.Fprint(w, "no id")
		w.WriteHeader(http.StatusBadRequest)
		return
	}*/

	if statementId != "" || voidedStatementId != "" {
		singleQuery(w, r, statementId, voidedStatementId)
		return
	} else {
		complexQuery(w, r)
		return
	}
}

func singleQuery(w http.ResponseWriter, r *http.Request, statementId string, voidedStatementId string) {
	parameters_NotAllowed := []string{"agent", "verb", "activity",
		"registration", "related_activities",
		"since", "until", "limit", "ascending"}
	for _, p := range parameters_NotAllowed {
		param := r.FormValue(p)
		if param != "" {
			// fmt.Fprint(w, "not allowed another filter")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	voided := false
	if voidedStatementId != "" {
		statementId = voidedStatementId
		voided = true
	}

	// connect to db
	session := dbSession()
	defer session.Close()
	statementsC := session.DB("LRS").C("statements")

	// find statement
	var result Statement
	err := statementsC.Find(bson.M{"id": statementId}).One(&result)
	if err != nil {
		//fmt.Fprint(w, err)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// The LRS MUST not return any Statement which has been voided,
	// unless that Statement has been requested by voidedStatementId.
	if result.Void != voided {
		//fmt.Fprint(w, "result: void=")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// return back found statement
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.Encode(result)

	return
}

func complexQuery(w http.ResponseWriter, r *http.Request) {

	var q = bson.M{}
	var tq = []bson.M{}
	var and = []bson.M{}
	var s = bson.M{}
	var u = bson.M{}

	if since := r.FormValue("since"); since != "" {
		t, _ := time.Parse("RFC3339", since)
		tq = append(tq, bson.M{"$gt": t})
		s = tq[0]
	}

	if until := r.FormValue("until"); until != "" {
		t, _ := time.Parse("RFC3339", until)
		tq = append(tq, bson.M{"$lt": t})
		u = tq[1]
	}

	if len(tq) == 2 {
		q["storedVal"] = bson.M{"$and": tq}
	} else if len(tq) == 1 {
		q["storedVal"] = s
	}

	findInRef := false
	if verb := r.FormValue("verb"); verb != "" {
		q["verb.id"] = verb
		findInRef = true
	}

	if registration := r.FormValue("registration"); registration != "" {
		q["context.registration"] = registration
		findInRef = true
	}

	if agent := r.FormValue("agent"); agent != "" {
		// JSONObject, Compare optional Identifiers
		var actor Actor
		decoder := json.NewDecoder(strings.NewReader(agent))
		err := decoder.Decode(&actor)
		if err != nil {
			//fmt.Fprint(w, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		aq := []bson.M{bson.M{"actor": actor},
			bson.M{"actor.member": actor}}

		findInRef = true

		related_agents := r.FormValue("related_agents")
		if related_agents != "" && related_agents == "true" {
			aq = append(aq, bson.M{"object": actor})
			aq = append(aq, bson.M{"context.instructor": actor})
			aq = append(aq, bson.M{"object.context.instructor": actor})
			aq = append(aq, bson.M{"object.context.team": actor})
		}

		and = append(and, bson.M{"$or": aq})
	}

	if activity := r.FormValue("activity"); activity != "" {

		activq := []bson.M{bson.M{"object.id": activity}}

		findInRef = true

		related_activities := r.FormValue("related_activities")
		if related_activities != "" && related_activities == "true" {
			// parent, grouping, category, other, for object or array
			//sub statements?
			activq = append(activq, bson.M{"context.contextActivities.parent.id": activity})
			activq = append(activq, bson.M{"context.contextActivities.grouping.id": activity})
			activq = append(activq, bson.M{"context.contextActivities.category.id": activity})
			activq = append(activq, bson.M{"context.contextActivities.other.id": activity})
			activq = append(activq, bson.M{"object.context.contextActivities.parent.id": activity})
			activq = append(activq, bson.M{"object.context.contextActivities.grouping.id": activity})
			activq = append(activq, bson.M{"object.context.contextActivities.category.id": activity})
			activq = append(activq, bson.M{"object.context.contextActivities.other.id": activity})
		}

		and = append(and, bson.M{"$or": activq})
	}

	if attachments := r.FormValue("attachments"); attachments != "" {
		//todo
	}

	order := "-storedVal"
	if ascending := r.FormValue("ascending"); ascending == "true" {
		order = "storedVal"
	}

	//how can I control formating?
	if format := r.FormValue("format"); format != "" {
		//todo
	}

	if len(and) != 0 {
		q["$and"] = and
	}

	// find all statements that refrence the found statments
	// requery with original query and appended query
	//findInRef = false
	if findInRef {
		session := dbSession()
		defer session.Close()
		statementsC := session.DB("LRS").C("statements")

		// find those that meet criteria
		// to then find those that refrence them within
		// the same time frame
		var result []Statement
		err := statementsC.Find(q).All(&result)
		if err != nil {
			//fmt.Fprint(w, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		qr := bson.M{}
		err = findStatementRefs(w, result, s, u, &qr)

		if err != nil {
			//fmt.Fprint(w, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if len(and) != 0 {
			and = append(and, qr)
			q["$and"] = and
		}
	}

	limit := r.FormValue("limit")
	if limit == "" {
		limit = "0"
	}

	//convert limit string to int
	i, err := strconv.Atoi(limit)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// connect to db
	session := dbSession()
	defer session.Close()
	statementsC := session.DB("LRS").C("statements")

	var result []Statement //[]map[string] interface{} //	  []Statement //
	err = statementsC.Find(q).Sort(order).
		Limit(i).
		All(&result)
	if err != nil {
		//fmt.Fprint(w, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if len(result) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// https://github.com/adlnet/ADL_LRS/blob/d86aa83ec5674982a233bae5a90df5288c8209d0/lrs/util/retrieve_statement.py
	// https://github.com/adlnet/xAPI-Spec/blob/master/xAPI.md#stmtapi
	// /lrs/util/req_process.py :143
	// based on "stored" time, subject to permissions and maximum list length.
	// create cache for more statements due to limit
	// format StatementResult {statements [], more IRL (link to more via querystring if "limit" set)} with list in newest stored first if "ascending" not set

	// return back found statement
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	enc.Encode(result)

}

func findStatementRefs(w http.ResponseWriter, stmtset []Statement, sinceq bson.M, untilq bson.M, q *bson.M) error {

	// stop searching for refrence of refrence if none left
	if len(stmtset) == 0 {
		return nil
	}

	// go through all statements that match criteria
	// and add to query to find anything that refrences' them
	qs := []bson.M{}
	for _, s := range stmtset {
		qs = append(qs, bson.M{"object.objectType": "statementRef", "object.id": s.Id})
	}
	qs = []bson.M{bson.M{"$or": qs}}

	//statements refrenced also must adhere to time frame
	if sinceq != nil && untilq != nil {
		qs = append(qs, sinceq)
		qs = append(qs, untilq)
	} else if sinceq != nil {
		qs = append(qs, sinceq)
	} else if untilq != nil {
		qs = append(qs, untilq)
	}

	// finally weed out voided statements in this lookup
	qs = append(qs, bson.M{"void": false})

	// connect to db
	session := dbSession()
	defer session.Close()
	statementsC := session.DB("LRS").C("statements")

	var result []Statement
	err := statementsC.Find(qs).Distinct("id", &result)
	if err != nil {
		return err
	}

	err = findStatementRefs(w, result, sinceq, untilq, q)
	if err != nil {
		return err
	}

	*q = bson.M{"$or": []bson.M{
		bson.M{"$and": qs},
		*q}}

	return nil
}

// https://github.com/adlnet/xAPI-Spec/blob/master/xAPI.md#stmtapi
// http://zackpierce.github.io/xAPI-Validator-JS/
// not sure how much if/howmuch I will validate structure
func (s *Statement) Validate() (string, int) {

	// generate new ID's
	if s.Id == "" {
		id, _ := uuid.NewV4()
		s.Id = id.String()
	}
	// generate stored datetime .Format("RFC3339")
	t := time.Now()
	s.StoredVal = t
	s.Stored = t.Format("RFC3339")

	return s.Id, 0
}

// -----------------------------------------------------------------
// import github.com/bitly/go-simplejson maybe instead
type Statement struct {
	//_Id bson.ObjectId `bson:"_id,-" json:"_id,-"`
	Id          string        `bson:"id,omitempty" json:"id,omitempty"`
	Void        bool          `json:"-"`
	Actor       *Actor        `bson:"actor,omitempty" json:"actor,omitempty"`
	Verb        Verb          `bson:"verb,omitempty" json:"verb,omitempty"`
	Object      *Object       `bson:"object,omitempty" json:"object,omitempty"`
	Result      *Result       `bson:"result,omitempty" json:"result,omitempty"`
	Context     *Context      `bson:"context,omitempty" json:"context,omitempty"`
	Timestamp   string        `bson:"timestamp,omitempty" json:"timestamp,omitempty"`
	Stored      string        `bson:"stored,omitempty" json:"stored,omitempty"`
	StoredVal   time.Time     `json:"-"`
	Authority   *Actor        `bson:"authority,omitempty" json:"authority,omitempty"`
	Version     string        `bson:"version,omitempty" json:"version,omitempty"`
	Attachments []*Attachment `bson:"attachments,omitempty" json:"attachments,omitempty"`
}

// statement
type Actor struct {
	ObjectType   string   `bson:"objectType,omitempty" json:"objectType,omitempty"`
	Name         string   `bson:"name,omitempty" json:"name,omitempty"`
	Mbox         string   `bson:"mbox,omitempty" json:"mbox,omitempty"`
	Mbox_sha1sum string   `bson:"mbox_sha1sum,omitempty" json:"mbox_sha1sum,omitempty"`
	OpenID       string   `bson:"openID,omitempty" json:"openID,omitempty"`
	Account      *Account `bson:"account,omitempty" json:"account,omitempty"`
	// group
	Member []*Actor `bson:"member,omitempty" json:"member,omitempty"`
}

// actor
type Agent struct {
	ObjectType   string   `bson:",omitempty" json:",omitempty"`
	Name         string   `bson:",omitempty" json:",omitempty"`
	Mbox         string   `bson:",omitempty" json:",omitempty"`
	Mbox_sha1sum string   `bson:",omitempty" json:",omitempty"`
	OpenID       string   `bson:",omitempty" json:",omitempty"`
	Account      *Account `bson:",omitempty" json:",omitempty"`
}

// actor
type Account struct {
	HomePage string `bson:"homePage,omitempty" json:"homePage,omitempty"`
	Name     string `bson:"name,omitempty" json:"name,omitempty"`
}

// statement
type Verb struct {
	Id      string            `bson:"id,omitempty" json:"id,omitempty"`
	Display map[string]string `bson:"display,omitempty" json:"display,omitempty"`
}

// activity, Agent/Group, Sub-Statement, StatementReference
type Object struct {
	ObjectType string `bson:"objectType,omitempty" json:"objectType,omitempty"`
	Id         string `bson:"id,omitempty" json:"id,omitempty"`
	// Agent
	Name         string   `bson:"name,omitempty" json:"name,omitempty"`
	Mbox         string   `bson:"mbox,omitempty" json:"mbox,omitempty"`
	Mbox_sha1sum string   `bson:"mbox_sha1sum,omitempty" json:"mbox_sha1sum,omitempty"`
	OpenID       string   `bson:"openID,omitempty" json:"openID,omitempty"`
	Account      *Account `bson:"account,omitempty" json:"account,omitempty"`
	// group
	Member []*Actor `bson:"member,omitempty" json:"member,omitempty"`
	// substatement
	Definition  *Definition   `bson:"definition,omitempty" json:"definition,omitempty"`
	Actor       *Actor        `bson:"actor,omitempty" json:"actor,omitempty"`
	Verb        *Verb         `bson:"verb,omitempty" json:"verb,omitempty"`
	Object      *StatementRef `bson:"object,omitempty" json:"object,omitempty"`
	Result      *Result       `bson:"result,omitempty" json:"result,omitempty"`
	Context     *Context      `bson:"context,omitempty" json:"context,omitempty"`
	Timestamp   string        `bson:"timestamp,omitempty" json:"timestamp,omitempty"`
	Stored      string        `bson:"stored,omitempty" json:"stored,omitempty"`
	StoredVal   time.Time     `bson:"storedVal,omitempty" json:"-"`
	Authority   *Actor        `bson:"authority,omitempty" json:"authority,omitempty"`
	Version     string        `bson:"version,omitempty" json:"version,omitempty"`
	Attachments []*Attachment `bson:"attachments,omitempty" json:"attachments,omitempty"`
}

// object
type Definition struct {
	Name                    map[string]string        `bson:"name,omitempty" json:"name,omitempty"`
	Description             map[string]string        `bson:"description,omitempty" json:"description,omitempty"`
	Type                    string                   `bson:"type,omitempty" json:"type,omitempty"`
	MoreInfo                string                   `bson:"moreInfo,omitempty" json:"moreInfo,omitempty"`
	InteractionType         string                   `bson:"interactionType,omitempty" json:"interactionType,omitempty"`
	CorrectResponsesPattern []string                 `bson:"correctResponsesPattern,omitempty" json:"correctResponsesPattern,omitempty"`
	Choices                 []*InteractionComponents `bson:"choices,omitempty" json:"choices,omitempty"`
	Scale                   []*InteractionComponents `bson:"scale,omitempty" json:"scale,omitempty"`
	Source                  []*InteractionComponents `bson:"source,omitempty" json:"source,omitempty"`
	Target                  []*InteractionComponents `bson:"target,omitempty" json:"target,omitempty"`
	Steps                   []*InteractionComponents `bson:"steps,omitempty" json:"steps,omitempty"`
	Extensions              map[string]interface{}   `bson:"extensions,omitempty" json:"extensions,omitempty"`
}

// definition
type Interaction struct {
	InteractionType         string                   `bson:"interactionType,omitempty" json:"interactionType,omitempty"`
	CorrectResponsesPattern []string                 `bson:"correctResponsesPattern,omitempty" json:"correctResponsesPattern,omitempty"`
	Choices                 []*InteractionComponents `bson:"choices,omitempty" json:"choices,omitempty"`
	Scale                   []*InteractionComponents `bson:"scale,omitempty" json:"scale,omitempty"`
	Source                  []*InteractionComponents `bson:"source,omitempty" json:"source,omitempty"`
	Target                  []*InteractionComponents `bson:"target,omitempty" json:"target,omitempty"`
	Steps                   []*InteractionComponents `bson:"steps,omitempty" json:"steps,omitempty"`
}

// interaction
type InteractionComponents struct {
	Id          string            `bson:"id,omitempty" json:"id,omitempty"`
	Description map[string]string `bson:"description,omitempty" json:"description,omitempty"`
}

// statement
type Result struct {
	Score      *Score                 `bson:"score,omitempty" json:"score,omitempty"`
	Success    bool                   `bson:"success,omitempty" json:"success,omitempty"`
	Completion bool                   `bson:"completion,omitempty" json:"completion,omitempty"`
	Response   string                 `bson:"response,omitempty" json:"response,omitempty"`
	Duration   string                 `bson:"duration,omitempty" json:"duration,omitempty"`
	Extensions map[string]interface{} `bson:"extensions,omitempty" json:"extensions,omitempty"`
}

// result
type Score struct {
	Scaled float32 `bson:"scaled" json:"scaled"`
	Raw    float32 `bson:"raw" json:"raw"`
	Min    float32 `bson:"min" json:"min"`
	Max    float32 `bson:"max" json:"max"`
}

// statement
type Context struct {
	Registration      string                 `bson:"registration,omitempty" json:"registration,omitempty"`
	Instructor        *Actor                 `bson:"instructor,omitempty" json:"instructor,omitempty"`
	Team              *Actor                 `bson:"team,omitempty" json:"team,omitempty"`
	ContextActivities *ContextActivities     `bson:"contextActivities,omitempty" json:"contextActivities,omitempty"`
	Revision          string                 `bson:"revision,omitempty" json:"revision,omitempty"`
	Platform          string                 `bson:"platform,omitempty" json:"platform,omitempty"`
	Language          string                 `bson:"language,omitempty" json:"language,omitempty"`
	Statement         *StatementRef          `bson:"statement,omitempty" json:"statement,omitempty"`
	Extensions        map[string]interface{} `bson:"extensions,omitempty" json:"extensions,omitempty"`
}

// contextActivities
type ContextActivities struct {
	Parent   []*Object `bson:"parent,omitempty" json:"parent,omitempty"`
	Grouping []*Object `bson:"grouping,omitempty" json:"grouping,omitempty"`
	Category []*Object `bson:"category,omitempty" json:"category,omitempty"`
	Other    []*Object `bson:"other,omitempty" json:"other,omitempty"`
}

// context
type StatementRef struct {
	ObjectType string      `bson:"objectType,omitempty" json:"objectType,omitempty"`
	Id         string      `bson:"id,omitempty" json:"id,omitempty"`
	Definition *Definition `bson:"definition,omitempty" json:"definition,omitempty"`
}

// statement
type Attachment struct {
	UsageType   string            `bson:"usageType,omitempty" json:"usageType,omitempty"`
	Display     map[string]string `bson:"display,omitempty" json:"display,omitempty"`
	Description map[string]string `bson:"description,omitempty" json:"description,omitempty"`
	ContentType string            `bson:"contentType,omitempty" json:"contentType,omitempty"`
	Length      int               `bson:"length,omitempty" json:"length,omitempty"`
	Sha2        string            `bson:"sha2,omitempty" json:"sha2,omitempty"`
	FileUrl     string            `bson:"fileUrl,omitempty" json:"fileUrl,omitempty"`
}
