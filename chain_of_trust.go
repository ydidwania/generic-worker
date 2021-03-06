package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/clearsign"
	"golang.org/x/crypto/openpgp/packet"

	"github.com/taskcluster/taskcluster-base-go/scopes"
	"github.com/taskcluster/taskcluster-client-go/queue"
)

const (
	ChainOfTrustKeyNotSecureMessage = "Was expecting attempt to read private chain of trust key as task user to fail - however, it did not!"
)

var (
	certifiedLogPath = filepath.Join("generic-worker", "certified.log")
	certifiedLogName = "public/logs/certified.log"
	signedCertPath   = filepath.Join("generic-worker", "chainOfTrust.json.asc")
	signedCertName   = "public/chainOfTrust.json.asc"
)

type ChainOfTrustFeature struct {
	PrivateKey *packet.PrivateKey
}

type ArtifactHash struct {
	SHA256 string `json:"sha256"`
}

type CoTEnvironment struct {
	PublicIPAddress  string `json:"publicIpAddress"`
	PrivateIPAddress string `json:"privateIpAddress"`
	InstanceID       string `json:"instanceId"`
	InstanceType     string `json:"instanceType"`
	Region           string `json:"region"`
}

type ChainOfTrustData struct {
	Version     int                          `json:"chainOfTrustVersion"`
	Artifacts   map[string]ArtifactHash      `json:"artifacts"`
	Task        queue.TaskDefinitionResponse `json:"task"`
	TaskID      string                       `json:"taskId"`
	RunID       uint                         `json:"runId"`
	WorkerGroup string                       `json:"workerGroup"`
	WorkerID    string                       `json:"workerId"`
	Environment CoTEnvironment               `json:"environment"`
}

type ChainOfTrustTaskFeature struct {
	task    *TaskRun
	privKey *packet.PrivateKey
}

func (feature *ChainOfTrustFeature) Name() string {
	return "Chain of Trust"
}

func (feature *ChainOfTrustFeature) PersistState() error {
	return nil
}

func (feature *ChainOfTrustFeature) Initialise() (err error) {
	feature.PrivateKey, err = readPrivateKey()
	if err != nil {
		return
	}

	// platform-specific mechanism to lock down file permissions
	// of private signing key
	err = secureSigningKey()
	return
}

func readPrivateKey() (privateKey *packet.PrivateKey, err error) {
	var privKeyFile *os.File
	privKeyFile, err = os.Open(config.SigningKeyLocation)
	if err != nil {
		return
	}
	defer privKeyFile.Close()
	var entityList openpgp.EntityList
	entityList, err = openpgp.ReadArmoredKeyRing(privKeyFile)
	if err != nil {
		return
	}
	privateKey = entityList[0].PrivateKey
	return
}

func (feature *ChainOfTrustFeature) IsEnabled(task *TaskRun) bool {
	return task.Payload.Features.ChainOfTrust
}

func (feature *ChainOfTrustFeature) NewTaskFeature(task *TaskRun) TaskFeature {
	return &ChainOfTrustTaskFeature{
		task:    task,
		privKey: feature.PrivateKey,
	}
}

func (feature *ChainOfTrustTaskFeature) ReservedArtifacts() []string {
	return []string{
		signedCertName,
		certifiedLogName,
	}
}

func (cot *ChainOfTrustTaskFeature) RequiredScopes() scopes.Required {
	// let's not require any scopes, as I see no reason to control access to this feature
	return scopes.Required{}
}

func (cot *ChainOfTrustTaskFeature) Start() *CommandExecutionError {
	// Return an error if the task user can read the private key file.
	// We shouldn't be able to read the private key, if we can let's raise
	// MalformedPayloadError, as it could be a problem with the task definition
	// (for example, enabling chainOfTrust on a worker type that has
	// runTasksAsCurrentUser enabled).
	err := cot.ensureTaskUserCantReadPrivateCotKey()
	if err != nil {
		return MalformedPayloadError(err)
	}
	return nil
}

func (cot *ChainOfTrustTaskFeature) Stop() *CommandExecutionError {
	logFile := filepath.Join(taskContext.TaskDir, logPath)
	certifiedLogFile := filepath.Join(taskContext.TaskDir, certifiedLogPath)
	signedCert := filepath.Join(taskContext.TaskDir, signedCertPath)
	e := copyFileContents(logFile, certifiedLogFile)
	if e != nil {
		panic(e)
	}
	err := cot.task.uploadLog(certifiedLogName, certifiedLogPath)
	if err != nil {
		return err
	}
	artifactHashes := map[string]ArtifactHash{}
	for _, artifact := range cot.task.Artifacts {
		switch a := artifact.(type) {
		case *S3Artifact:
			// make sure SHA256 is calculated
			hash, err := calculateHash(a)
			if err != nil {
				panic(err)
			}
			artifactHashes[a.Name] = ArtifactHash{
				SHA256: hash,
			}
		}
	}

	cotCert := &ChainOfTrustData{
		Version:     1,
		Artifacts:   artifactHashes,
		Task:        cot.task.Definition,
		TaskID:      cot.task.TaskID,
		RunID:       cot.task.RunID,
		WorkerGroup: config.WorkerGroup,
		WorkerID:    config.WorkerID,
		Environment: CoTEnvironment{
			PublicIPAddress:  config.PublicIP.String(),
			PrivateIPAddress: config.PrivateIP.String(),
			InstanceID:       config.InstanceID,
			InstanceType:     config.InstanceType,
			Region:           config.Region,
		},
	}

	certBytes, e := json.MarshalIndent(cotCert, "", "  ")
	if e != nil {
		panic(e)
	}
	// separate signature from json with a new line
	certBytes = append(certBytes, '\n')

	in := bytes.NewBuffer(certBytes)
	out, e := os.Create(signedCert)
	if e != nil {
		panic(e)
	}
	defer out.Close()

	w, e := clearsign.Encode(out, cot.privKey, nil)
	if e != nil {
		panic(e)
	}
	_, e = io.Copy(w, in)
	if e != nil {
		panic(e)
	}
	w.Close()
	out.Write([]byte{'\n'})
	out.Close()
	err = cot.task.uploadLog(signedCertName, signedCertPath)
	if err != nil {
		return err
	}
	return nil
}

func calculateHash(artifact *S3Artifact) (hash string, err error) {
	rawContentFile := filepath.Join(taskContext.TaskDir, artifact.Path)
	rawContent, err := os.Open(rawContentFile)
	if err != nil {
		return
	}
	defer rawContent.Close()
	hasher := sha256.New()
	_, err = io.Copy(hasher, rawContent)
	if err != nil {
		panic(err)
	}
	hash = hex.EncodeToString(hasher.Sum(nil))
	return
}
