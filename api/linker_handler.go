package api

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os/exec"
	"strings"

	"github.com/fsouza/go-dockerclient"
)

var dockerClient, dockerErr = docker.NewClient("unix:///var/run/docker.sock")

const (
	linkerApiPrefix           = "/api/linker"
	linkerDockerIdApiPrefix   = "/api/linker/dockerid"
	linkerDockerInfoApiPrefix = "/api/linker/dockerinfo"
	mesos_path                = "/tmp/mesos/slaves"
	stdout_const              = "stdout"
	runs_const                = "runs"
)

func HandleLinkerRequest(w http.ResponseWriter, r *http.Request) error {
	request := r.URL.Path

	if strings.HasPrefix(request, linkerDockerIdApiPrefix) {
		HandleLinkerDockerIdRequest(w, r)
		return nil
	}

	if strings.HasPrefix(request, linkerDockerInfoApiPrefix) {
		HandleLinkerDockerInfoRequest(w, r)
		return nil
	}

	return nil
}

func HandleLinkerDockerIdRequest(w http.ResponseWriter, r *http.Request) error {
	taskid := r.FormValue("taskid")
	fmt.Printf("taskid is %s \n", taskid)
	if len(taskid) != 0 {
		output, err := ParseDockerName(taskid)
		fmt.Printf("output is %s \n", output)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return nil
		} else {
			dockerid := ParseDockerNameString(output)
			fmt.Printf("dockerid is %s \n", dockerid)
			//			w.Write([]byte(dockerid))
			fmt.Fprintf(w, "%s", dockerid)
			return nil
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		return nil
	}
}

func HandleLinkerDockerInfoRequest(w http.ResponseWriter, r *http.Request) error {
	taskid := r.FormValue("taskid")
	slaveid := r.FormValue("slaveid")
	fmt.Printf("DockerInfo taskid is %s \n", taskid)

	if dockerErr != nil {
		fmt.Printf("Cant't create docker client. %v", dockerErr)
		w.WriteHeader(http.StatusInternalServerError)
		return nil
	}

	if len(taskid) != 0 && len(slaveid) != 0 {
		output, err := ParseDockerName(taskid)
		fmt.Printf("output is %s \n", output)
		if err != nil {
			fmt.Printf("Cant't parse docker name. %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return nil
		} else {
			dockerid := ParseDockerNameString(output)
			containerInfo, err1 := dockerClient.InspectContainer("mesos-" + slaveid + "." + dockerid)
			if err1 != nil {
				fmt.Printf("Cant't inspect docker container. %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return nil
			}

			out, err := json.Marshal(containerInfo)
			if err != nil {
				fmt.Printf("failed to marshall response %+v with error: %s", containerInfo, err)
				w.WriteHeader(http.StatusInternalServerError)
				return nil
			}

			w.Header().Set("Content-Type", "application/json")
			w.Write(out)
			//			fmt.Printf("dockerid is %s \n", dockerid)
			//			w.Write([]byte(dockerid))
			//			fmt.Fprintf(w, "%s", dockerid)
			return nil
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		return nil
	}
}

func ParseDockerName(taskid string) (dockername string, err error) {
	cmdText := "find " + mesos_path + " | grep " + taskid + " | grep " + stdout_const + " | sed -n '1p;'"
	output, _, err := ExecCommand(cmdText)
	return output, err
}

func ParseDockerNameString(source string) (output string) {
	if len(source) != 0 {
		array := strings.Split(source, runs_const)
		if len(array) != 0 {
			array = strings.Split(array[len(array)-1], "/")
			if len(array) != 0 {
				return array[1]
			}
		}
	}
	return ""
}

func ExecCommand(input string) (output string, errput string, err error) {
	var retoutput string
	var reterrput string
	cmd := exec.Command("/bin/bash", "-c", input)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", "", err
	}

	if err := cmd.Start(); err != nil {
		return "", "", err
	}

	bytesErr, err := ioutil.ReadAll(stderr)
	if err != nil {
		return "", "", err
	}

	if len(bytesErr) != 0 {
		reterrput = strings.Trim(string(bytesErr), "\n")
	}

	bytes, err := ioutil.ReadAll(stdout)
	if err != nil {
		return "", reterrput, err
	}

	if len(bytes) != 0 {
		retoutput = strings.Trim(string(bytes), "\n")
	}

	if err := cmd.Wait(); err != nil {
		return retoutput, reterrput, err
	}

	return retoutput, reterrput, err
}
