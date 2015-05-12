package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	api "github.com/GoogleCloudPlatform/kubernetes/pkg/api/v1beta3"
	"golang.org/x/build/buildlet"
	"golang.org/x/build/dashboard"
)

const (
	kubeEndpoint = "https://104.197.58.176/api/v1beta3"
	kubeToken    = "bIYSj24VILMDYDxg"
)

var kubePool = &kubeBuildletPool{}

var _ BuildletPool = (*kubeBuildletPool)(nil)

type kubeBuildletPool struct {
}

func (p *kubeBuildletPool) GetBuildlet(typ, rev string, el eventTimeLogger) (*buildlet.Client, error) {
	podName := "buildlet-" + typ + "-" + rev[:8] + "-rn" + randHex(6)
	conf, ok := dashboard.Builders[typ]
	if !ok || conf.DockerImage == "" {
		return nil, fmt.Errorf("invalid builder type %q", typ)
	}
	url := kubeEndpoint + "/namespaces/default/pods"
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(&api.Pod{
		TypeMeta: api.TypeMeta{
			APIVersion: "v1beta3",
			Kind:       "Pod",
		},
		ObjectMeta: api.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				"name": podName,
				"type": typ,
				"rev":  rev,
			},
		},
		Spec: api.PodSpec{
			RestartPolicy: "Never",
			Containers: []api.Container{
				{
					Name:    "buildlet",
					Image:   conf.DockerImage,
					Command: []string{"/usr/local/bin/stage0"},
					Env: []api.EnvVar{
						{
							Name:  "META_BUILDLET_BINARY_URL",
							Value: conf.BuildletBinaryURL(),
						},
					},
					ImagePullPolicy: api.PullAlways,
				},
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to encode pod in json: %v", err)
	}
	r, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: POST %q : %v", url, err)
	}
	r.Header.Add("Authorization", "Bearer "+kubeToken)
	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	el.logEventTime("creating_pod")
	res, err := client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: POST %q: %v", url, err)
	}
	if res.StatusCode != http.StatusCreated {
		body, _ := ioutil.ReadAll(res.Body)
		return nil, fmt.Errorf("http error: %d POST %q: %q: %v", res.StatusCode, url, string(body), err)
	}
	el.logEventTime("pod_created")
	var pod api.Pod
	if err := json.NewDecoder(res.Body).Decode(&pod); err != nil {
		return nil, fmt.Errorf("failed to decode pod resources: %v", err)
	}
	el.logEventTime("waiting_for_pod_running")
	for pod.Status.Phase == "Pending" {
		getURL := url + "/" + pod.Name
		r, err := http.NewRequest("GET", getURL, nil)
		r.Header.Add("Authorization", "Bearer "+kubeToken)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: GET %q : %v", getURL, err)
		}
		res, err := client.Do(r)
		if err != nil {
			return nil, fmt.Errorf("failed to make request: GET %q: %v", getURL, err)
		}
		if res.StatusCode != http.StatusOK {
			body, _ := ioutil.ReadAll(res.Body)
			return nil, fmt.Errorf("http error %d GET %q: %q: %v", res.StatusCode, getURL, string(body), err)
		}
		if err := json.NewDecoder(res.Body).Decode(&pod); err != nil {
			return nil, fmt.Errorf("failed to decode pod resources: %v", err)
		}
		time.Sleep(1 * time.Second)
	}
	if pod.Status.Phase != "Running" {
		return nil, fmt.Errorf("failed to start pod: %q: %q", pod.Status.Phase, pod.Status.Message)
	}
	el.logEventTime("pod_running")

	el.logEventTime("waiting_for_buidlet")
	buildletURL := "http://" + pod.Status.PodIP
	for {
		res, err := http.Get(buildletURL)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("http buildlet error: %d GET %q", res.StatusCode, buildletURL)
		}
		break
	}
	el.logEventTime("buildlet_ready")
	return buildlet.NewClient(pod.Status.PodIP+":80", buildlet.KeyPair{}), nil
}

func (p *kubeBuildletPool) String() string {
	return fmt.Sprintf("not yet implemented")
}
