package awsexpvar

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"
)

const metadataURL = "http://169.254.169.254/latest/meta-data/"
const taskRoleURL = "http://169.254.170.2"
const instanceIdentURL = "http://169.254.169.254/latest/dynamic/instance-identity/document"
const userDataURL = "http://169.254.169.254/latest/user-data"

// Logger is optional and allows logging errors closing local request bodies
type Logger interface {
	Log(keyvals ...interface{})
}

// Expvar allows exposing ECS and EC2 metadata on expvar
type Expvar struct {
	Log    Logger
	Client *http.Client
}

func (e *Expvar) client() *http.Client {
	if e.Client == nil {
		return http.DefaultClient
	}
	return e.Client
}

type availableCommandResponse struct {
	AvailableCommands []string `json:"AvailableCommands"`
}

// Var creates the expvar you should expose
func (e *Expvar) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		ret := make(map[string]interface{}, 5)
		ret["meta-data"] = e.metaData()
		ret["ecs-metadata"] = e.ecs()
		ret["instance-identity"] = e.instanceIdentity()
		ret["user-data"] = e.userData()
		ret["container-metadata"] = e.containerMetadata()
		return filterNil(ret)
	})
}

func filterNil(r map[string]interface{}) map[string]interface{} {
	ret := make(map[string]interface{}, len(r))
	for k, v := range r {
		if v == nil {
			continue
		}
		ret[k] = v
	}
	return ret
}

func (e *Expvar) containerMetadata() interface{} {
	metadataFile := os.Getenv("ECS_CONTAINER_METADATA_FILE")
	if metadataFile == "" {
		return nil
	}
	fileBytes, err := ioutil.ReadFile(metadataFile)
	if err != nil {
		return err
	}
	asObj := make(map[string]interface{}, 5)
	if err := json.Unmarshal(fileBytes, &asObj); err != nil {
		return err
	}
	return asObj
}

func (e *Expvar) userData() interface{} {
	val, err := e.single(userDataURL)
	if err != nil {
		return nil
	}
	return val
}

func (e *Expvar) instanceIdentity() interface{} {
	val, err := e.single(instanceIdentURL)
	if err != nil {
		return nil
	}
	return val
}

func (e *Expvar) metaData() interface{} {
	val, err := e.recurse(metadataURL)
	if err != nil {
		return nil
	}
	return val
}

func (e *Expvar) ecs() interface{} {
	ecsURL := e.ecsURL()
	if ecsURL == "" {
		return nil
	}
	val, err := e.recurse(ecsURL)
	if err != nil {
		return err
	}
	if asMap, ok := val.(map[string]interface{}); ok {
		taskRole := e.taskRole()
		asMap["RoleArn"] = taskRole
	}
	return val
}

type metadataTask struct {
	Arn           string
	DesiredStatus string
	KnownStatus   string
	Family        string
	Version       string
	Containers    []metadataContainer
}

type metadataContainer struct {
	DockerID   string `json:"DockerId"`
	DockerName string
	Name       string
}

type tasksEndpoint struct {
	Tasks []metadataTask
}

func (e *Expvar) httpGet(base string) (*http.Response, error) {
	req, err := http.NewRequest("GET", base, nil)
	if err != nil {
		return nil, err
	}

	ctx, onDone := context.WithTimeout(context.Background(), time.Millisecond*200)
	defer onDone()
	req = req.WithContext(ctx)
	return e.client().Do(req)
}

func (e *Expvar) taskRole() string {
	credURL := os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI")
	if credURL == "" {
		return "(no-relative-url-for-task-information)"
	}
	singleVal, err := e.single(taskRoleURL + credURL)
	if err != nil {
		return err.Error()
	}
	if asMap, ok := singleVal.(map[string]string); ok {
		return asMap["RoleArn"]
	}
	return "<invalid_single_value>"
}

func (e *Expvar) single(base string) (interface{}, error) {
	resp, err := e.httpGet(base)
	if err != nil {
		return nil, err
	}
	defer e.closeBody(resp)
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("not found")
	}
	var b []byte
	if b, err = ioutil.ReadAll(resp.Body); err != nil {
		return nil, err
	}
	respBody := string(b)
	m := map[string]string{}
	if err := json.Unmarshal([]byte(respBody), &m); err == nil {
		clearOut(m, "Token")
		clearOut(m, "AccessKeyId")
		clearOut(m, "SecretAccessKey")
		return m, nil
	}
	t := tasksEndpoint{}
	if err := json.Unmarshal([]byte(respBody), &t); err == nil && len(t.Tasks) > 0 {
		return t, nil
	}
	return respBody, nil
}

func clearOut(m map[string]string, key string) {
	if _, exists := m[key]; exists {
		m[key] = "(removed)"
	}
}

func (e *Expvar) recurse(base string) (interface{}, error) {
	ret := make(map[string]interface{})
	resp, err := e.httpGet(base)
	if err != nil {
		return nil, err
	}
	defer e.closeBody(resp)
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("not found")
	}
	var b []byte
	if b, err = ioutil.ReadAll(resp.Body); err != nil {
		return nil, err
	}
	respBody := string(b)
	// Try availableCommandResponse for sub commands
	var m availableCommandResponse
	if err := json.Unmarshal([]byte(respBody), &m); err == nil {
		if len(m.AvailableCommands) > 0 {
			for _, subCommand := range m.AvailableCommands {
				if subCommand == "/license" {
					continue
				}
				val, err := e.single(base + subCommand)
				if err != nil {
					ret[subCommand] = err
				} else {
					ret[subCommand] = val
				}
			}
			return ret, nil
		}
	}
	// Got an object back.  Is it a link to more sub directories, or is it the end.  We don't know.
	parts := strings.Split(respBody, "\n")
	e.processParts(base, parts, ret)
	return ret, nil
}

func (e *Expvar) processParts(base string, parts []string, ret map[string]interface{}) {
	for _, part := range parts {
		if part == "" {
			continue
		}
		if part == "security-credentials/" {
			continue
		}
		if !strings.HasSuffix(part, "/") {
			val, err := e.single(base + "/" + part)
			if err != nil {
				ret[part] = err
			} else {
				ret[part] = val
			}
			continue
		}
		val, err := e.recurse(base + "/" + part)
		if err != nil {
			ret[part] = err
		} else {
			ret[part] = val
		}
	}
}

func (e *Expvar) localIP() string {
	resp, err := e.httpGet("http://169.254.169.254/latest/meta-data/local-ipv4/")
	if err != nil {
		return ""
	}
	defer e.closeBody(resp)
	localIP, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(localIP)
}

func (e *Expvar) ecsURL() string {
	ip := e.localIP()
	if ip == "" {
		return ""
	}
	return "http://" + ip + ":51678"
}

func (e *Expvar) closeBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil && e.Log != nil {
		e.Log.Log("err", err, "error ending body")
	}
}
