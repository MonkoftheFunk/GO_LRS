/**
 * GO_LRS
 * https://github.com/MonkoftheFunk/GO_LRS
 */

package main

import (
	"encoding/json"
	//"fmt"
	"github.com/gorilla/mux"
	"github.com/nu7hatch/gouuid"
	"io"
	"io/ioutil"
	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	"net/http"
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

func readStmts(r io.Reader) (bool, string, error) {
	body, err := ioutil.ReadAll(r)
	if err != nil {
		return false, string(body), err
	}

	isArray := false
	for _, c := range body {
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			continue
		}
		isArray = c == '['
		break
	}

	return isArray, string(body), err
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

	// determin if array of statements or just single statement
	isArray, body, err := readStmts(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	decoder := json.NewDecoder(strings.NewReader(body))

	var statements []Statement
	if isArray {
		err := decoder.Decode(&statements)
		if err != nil {
			//fmt.Fprint(w, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	} else {
		var statement Statement
		err := decoder.Decode(&statement)
		if err != nil {
			// fmt.Fprint(w, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		statements = append(statements, statement)
	}

	// connect to db
	session := dbSession()
	defer session.Close()
	statementsC := session.DB("LRS").C("statements")

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

	decoder := json.NewDecoder(r.Body)

	var s Statement
	err := decoder.Decode(&s)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		// fmt.Fprint(w, "decode error")
		// fmt.Fprint(w, err)
		return
	}

	// connect to db
	session := dbSession()
	defer session.Close()
	statementsC := session.DB("LRS").C("statements")

	// check if trying to replace object with same id
	s.Id = statementId
	status := checkIdConflict(w, statementsC, s)
	if status != 0 {
		w.WriteHeader(status)
		// fmt.Fprint(w, "confilct "+string(status))
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
			return 0
		}

		// kludge the result has "stored" so add field for comparison
		statement.Stored = result.Stored

		// can't compare structs with arrays/maps
		rj, _ := json.Marshal(result)
		sj, _ := json.Marshal(statement)

		if string(rj) != string(sj) {
			return http.StatusConflict
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
	if statementId == "" && voidedStatementId == "" {
		//fmt.Fprint(w, "no id")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// if single query
	// then make sure other parameters are not called
	if statementId != "" || voidedStatementId != "" {
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
	} else {
		// complex query
		var q = bson.M{}
		var tq = []bson.M{}

		if since := r.FormValue("since"); since != "" {
			t, _ := time.Parse("RFC3339", since)
			tq = append(tq, bson.M{"$gt": t})
		}

		if until := r.FormValue("until"); until != "" {
			t, _ := time.Parse("RFC3339", until)
			tq = append(tq, bson.M{"$lt": t})
		}

		if len(tq) == 2 {
			q["$and"] = []bson.M{tq[0], tq[1]}
		} else if len(tq) == 1 {
			q["StoredVal"] = tq[0]
		}

		findInRef := false
		if verb := r.FormValue("verb"); verb != "" {
			q["Verb"] = verb
			findInRef = true
		}

		if registration := r.FormValue("registration"); registration != "" {
			q["Registration"] = registration
			findInRef = true
		}

		if agent := r.FormValue("agent"); agent != "" {
			// JSONObject, Compare optional Identifiers
			var actor Actor
			decoder := json.NewDecoder(strings.NewReader(agent))
			err := decoder.Decode(&actor)
			if err != nil {
				// fmt.Fprint(w, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			aq := []bson.M{bson.M{"Actor": actor},
				bson.M{"Actor.Member": actor}}

			findInRef = true

			related_agents := r.FormValue("related_agents")
			if related_agents != "" && related_agents == "true" {
				aq = append(aq, bson.M{"Object.Actor": actor})
				aq = append(aq, bson.M{"Context.Instructor": actor})
				aq = append(aq, bson.M{"Object.Context.Instructor": actor})
				aq = append(aq, bson.M{"Object.Context.Team": actor})
			}

			q["$or"] = aq
		}

		if activity := r.FormValue("activity"); activity != "" {

			activq := []bson.M{bson.M{"Object.Id": activity}}

			findInRef = true

			related_activities := r.FormValue("related_activities")
			if related_activities != "" && related_activities == "true" {
				activq = append(activq, bson.M{"Context.ContextActivities": activity})
				activq = append(activq, bson.M{"Object.Context.ContextActivities": activity})
			}

			q["$or"] = activq
		}

		if attachments := r.FormValue("attachments"); attachments != "" {
		}

		order := "-StoredVal"
		if ascending := r.FormValue("ascending"); ascending == "true" {
			order = "StoredVal"
		}

		//how can I control formating?
		if format := r.FormValue("format"); format != "" {
		}

		// find all statements that refrence the found statments
		// requery with original query and appended query
		if findInRef {
			//
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

		var result []Statement
		err = statementsC.Find(q).Sort(order).
			Limit(i).
			All(&result)
		if err != nil {
			// fmt.Fprint(w, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// https://github.com/adlnet/ADL_LRS/blob/d86aa83ec5674982a233bae5a90df5288c8209d0/lrs/util/retrieve_statement.py
		// https://github.com/adlnet/xAPI-Spec/blob/master/xAPI.md#stmtapi
		// /lrs/util/req_process.py :143
		// based on "stored" time, subject to permissions and maximum list length.
		// create cache for more statements due to limit
		// format StatementResult {statements [], more IRL (link to more via querystring if "limit" set)} with list in newest stored first if "ascending" not set

		// return 200 statementResults with proper header
	}
}

/*
 * def findstmtrefs(stmtset, sinceq, untilq):
    if stmtset.count() == 0:
        return stmtset
    q = Q()
    for s in stmtset:
        q = q | Q(object_statementref__ref_id=s.statement_id)

    if sinceq and untilq:
        q = q & Q(sinceq, untilq)
    elif sinceq:
        q = q & sinceq
    elif untilq:
        q = q & untilq
    # finally weed out voided statements in this lookup
    q = q & Q(voided=False)
    return findstmtrefs(models.Statement.objects.filter(q).distinct(), sinceq, untilq) | stmtset
*/

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
	Id          string       `bson:"id,omitempty" json:"id,omitempty"`
	Void        bool         `json:"-"`
	Actor       Actor        `bson:"actor,omitempty" json:"actor,omitempty"`
	Verb        Verb         `bson:"verb,omitempty" json:"verb,omitempty"`
	Object      Object       `bson:"object,omitempty" json:"object,omitempty"`
	Result      Result       `bson:"result,omitempty" json:"result,omitempty"`
	Context     Context      `bson:"context,omitempty" json:"context,omitempty"`
	Timestamp   string       `bson:"timestamp,omitempty" json:"timestamp,omitempty"`
	Stored      string       `bson:"stored,omitempty" json:"stored,omitempty"`
	StoredVal   time.Time    `bson:"storedVal,omitempty" json:"-"`
	Authority   Actor        `bson:"authority,omitempty" json:"authority,omitempty"`
	Version     string       `bson:"version,omitempty" json:"version,omitempty"`
	Attachments []Attachment `bson:"attachments,omitempty" json:"attachments,omitempty"`
}

// statement
type Actor struct {
	ObjectType   string  `bson:"objectType,omitempty" json:"objectType,omitempty"`
	Name         string  `bson:"name,omitempty" json:"name,omitempty"`
	Mbox         string  `bson:"mbox,omitempty" json:"mbox,omitempty"`
	Mbox_sha1sum string  `bson:"mbox_sha1sum,omitempty" json:"mbox_sha1sum,omitempty"`
	OpenID       string  `bson:"openID,omitempty" json:"openID,omitempty"`
	Account      Account `bson:"account,omitempty" json:"account,omitempty"`
	// group
	Member []Actor `bson:"member,omitempty" json:"member,omitempty"`
}

// actor
type Agent struct {
	ObjectType   string  `bson:",omitempty" json:",omitempty"`
	Name         string  `bson:",omitempty" json:",omitempty"`
	Mbox         string  `bson:",omitempty" json:",omitempty"`
	Mbox_sha1sum string  `bson:",omitempty" json:",omitempty"`
	OpenID       string  `bson:",omitempty" json:",omitempty"`
	Account      Account `bson:",omitempty" json:",omitempty"`
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
	ObjectType string     `bson:"objectType,omitempty" json:"objectType,omitempty"`
	Id         string     `bson:"id,omitempty" json:"id,omitempty"`
	Definition Definition `bson:"definition,omitempty" json:"definition,omitempty"`
	// substatement
	Actor       Actor        `bson:"actor,omitempty" json:"actor,omitempty"`
	Verb        Verb         `bson:"verb,omitempty" json:"verb,omitempty"`
	Object      StatementRef `bson:"object,omitempty" json:"object,omitempty"`
	Result      Result       `bson:"result,omitempty" json:"result,omitempty"`
	Context     Context      `bson:"context,omitempty" json:"context,omitempty"`
	Timestamp   string       `bson:"timestamp,omitempty" json:"timestamp,omitempty"`
	Stored      string       `bson:"stored,omitempty" json:"stored,omitempty"`
	StoredVal   time.Time    `bson:"storedVal,omitempty" json:"-"`
	Authority   Actor        `bson:"authority,omitempty" json:"authority,omitempty"`
	Version     string       `bson:"version,omitempty" json:"version,omitempty"`
	Attachments []Attachment `bson:"attachments,omitempty" json:"attachments,omitempty"`
}

// object
type Definition struct {
	Name                    map[string]string       `bson:"name,omitempty" json:"name,omitempty"`
	Description             map[string]string       `bson:"description,omitempty" json:"description,omitempty"`
	Type                    string                  `bson:"type,omitempty" json:"type,omitempty"`
	MoreInfo                string                  `bson:"moreInfo,omitempty" json:"moreInfo,omitempty"`
	InteractionType         string                  `bson:"interactionType,omitempty" json:"interactionType,omitempty"`
	CorrectResponsesPattern []string                `bson:"correctResponsesPattern,omitempty" json:"correctResponsesPattern,omitempty"`
	Choices                 []InteractionComponents `bson:"choices,omitempty" json:"choices,omitempty"`
	Scale                   []InteractionComponents `bson:"scale,omitempty" json:"scale,omitempty"`
	Source                  []InteractionComponents `bson:"source,omitempty" json:"source,omitempty"`
	Target                  []InteractionComponents `bson:"target,omitempty" json:"target,omitempty"`
	Steps                   []InteractionComponents `bson:"steps,omitempty" json:"steps,omitempty"`
	Extensions              map[string]interface{}  `bson:"extensions,omitempty" json:"extensions,omitempty"`
}

// definition
type Interaction struct {
	InteractionType         string                  `bson:"interactionType,omitempty" json:"interactionType,omitempty"`
	CorrectResponsesPattern []string                `bson:"correctResponsesPattern,omitempty" json:"correctResponsesPattern,omitempty"`
	Choices                 []InteractionComponents `bson:"choices,omitempty" json:"choices,omitempty"`
	Scale                   []InteractionComponents `bson:"scale,omitempty" json:"scale,omitempty"`
	Source                  []InteractionComponents `bson:"source,omitempty" json:"source,omitempty"`
	Target                  []InteractionComponents `bson:"target,omitempty" json:"target,omitempty"`
	Steps                   []InteractionComponents `bson:"steps,omitempty" json:"steps,omitempty"`
}

// interaction
type InteractionComponents struct {
	Id          string            `bson:"id,omitempty" json:"id,omitempty"`
	Description map[string]string `bson:"description,omitempty" json:"description,omitempty"`
}

// statement
type Result struct {
	Score      Score                  `bson:"score,omitempty" json:"score,omitempty"`
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
	Instructor        Actor                  `bson:"instructor,omitempty" json:"instructor,omitempty"`
	Team              Actor                  `bson:"team,omitempty" json:"team,omitempty"`
	ContextActivities map[string]interface{} `bson:"contextActivities,omitempty" json:"contextActivities,omitempty"`
	Revision          string                 `bson:"revision,omitempty" json:"revision,omitempty"`
	Platform          string                 `bson:"platform,omitempty" json:"platform,omitempty"`
	Language          string                 `bson:"language,omitempty" json:"language,omitempty"`
	Statement         StatementRef           `bson:"statement,omitempty" json:"statement,omitempty"`
	Extensions        map[string]interface{} `bson:"extensions,omitempty" json:"extensions,omitempty"`
}

// context
type StatementRef struct {
	ObjectType string     `bson:"objectType,omitempty" json:"objectType,omitempty"`
	Id         string     `bson:"id,omitempty" json:"id,omitempty"`
	Definition Definition `bson:"definition,omitempty" json:"definition,omitempty"`
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
