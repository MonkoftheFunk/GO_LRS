Tin Can / xAPI Learning Record Store in "Go"
=============================================

Currently starting with the Statement API
Refactoring and Tests will come later (looking at http://frisbyjs.com/ for a platform independent xAPI REST unit tests)

Packages used: (add issue if you want me to add to repository)
```
// routing
$ go get github.com/gorilla/mux

// guid creation
$ go get github.com/nu7hatch/gouuid

// monogdb (requires bzr to download)
$ go get labix.org/v2/mgo
$ go get labix.org/v2/mgo/bson
```
Done:
* POST statement(s) w/ conflict check
* PUT statement w/ conflict check
* POST/PUT Void statement
* GET statement by voidStatementId and StatementId

In Progress:
* GET complex query
* GET more query due to limit
* [REST API Tests](https://github.com/MonkoftheFunk/LRS_Validator)
* Deal with attachments
* Auth and Cross Origin Requests
* Concurrency
* Statement Validation

Not production ready

Developed because:
* Right tool for the job (fast and few system setup requirements)
* Can run on Windows and Linux servers or Google App Engine
* Wanted to start an Open Source Project
* Wanted to learn GO and MonogoDB
* The front end can then be written in any language (PHP, .Net, ...)
* Can be re-developed in any language easily

Lots of help from these resources:
* [Golang spec doc](http://golang.org/ref/spec)
* [xAPI spec doc](https://github.com/adlnet/xAPI-Spec/blob/master/xAPI.md)
* [ADL's Python LRS](https://github.com/adlnet/ADL_LRS)
* [Requires MongoDB](http://www.mongodb.org/)
* [LRS Requirements](https://github.com/creighton/try_git/blob/master/lrs_requirements.md)

Other Useful Resources:
* [REST API Testing tool](http://www.getpostman.com/)
* [GO IDE for Windows](http://www.zeusedit.com/)
* [Linked In Tin Can API User Group](http://www.linkedin.com/groups/Tin-Can-API-User-Group-4525548)
* [Tin Can API.com](http://tincanapi.com/)
* [Tin Can API .co .uk](http://tincanapi.co.uk/)
* [Bazaar is a version control to download mgo](http://bazaar.canonical.com/en/)
* [skelterjohn/rerun/](https://github.com/skelterjohn/rerun) Very useful, listens for file changes and compiles and closes and re-runs go app. (package uses [howeyc/fsnotify](https://github.com/howeyc/fsnotify))
## License

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
