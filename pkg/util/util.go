//
// Copyright (c) 2012-2019 Red Hat, Inc.
// This program and the accompanying materials are made
// available under the terms of the Eclipse Public License 2.0
// which is available at https://www.eclipse.org/legal/epl-2.0/
//
// SPDX-License-Identifier: EPL-2.0
//
// Contributors:
//   Red Hat, Inc. - initial API and implementation
//
package util

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	orgv1 "github.com/eclipse-che/che-operator/pkg/apis/org/v1"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/yaml"
)

var (
	k8sclient                    = GetK8Client()
	IsOpenShift, IsOpenShift4, _ = DetectOpenShift()
)

func ContainsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func DoRemoveString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

func GeneratePasswd(stringLength int) (passwd string) {
	rand.Seed(time.Now().UnixNano())
	chars := []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"abcdefghijklmnopqrstuvwxyz" +
		"0123456789")
	length := stringLength
	buf := make([]rune, length)
	for i := range buf {
		buf[i] = chars[rand.Intn(len(chars))]
	}
	passwd = string(buf)
	return passwd
}

func MapToKeyValuePairs(m map[string]string) string {
	buff := new(bytes.Buffer)
	keys := make([]string, 0, len(m))

	for key := range m {
		keys = append(keys, key)
	}

	sort.Strings(keys) //sort keys alphabetically

	for _, key := range keys {
		fmt.Fprintf(buff, "%s=%s,", key, m[key])
	}
	return strings.TrimSuffix(buff.String(), ",")
}

func DetectOpenShift() (isOpenshift bool, isOpenshift4 bool, anError error) {
	tests := IsTestMode()
	if tests {
		openshiftVersionEnv := os.Getenv("OPENSHIFT_VERSION")
		openshiftVersion, err := strconv.ParseInt(openshiftVersionEnv, 0, 64)
		if err == nil && openshiftVersion == 4 {
			return true, true, nil
		}
		return true, false, nil
	}

	apiGroups, err := getApiList()
	if err != nil {
		return false, false, err
	}
	for _, apiGroup := range apiGroups {
		if apiGroup.Name == "route.openshift.io" {
			isOpenshift = true
		}
		if apiGroup.Name == "config.openshift.io" {
			isOpenshift4 = true
		}
	}

	return isOpenshift, isOpenshift4, nil
}

func getDiscoveryClient() (*discovery.DiscoveryClient, error) {
	kubeconfig, err := config.GetConfig()
	if err != nil {
		return nil, err
	}
	return discovery.NewDiscoveryClientForConfig(kubeconfig)
}

func getApiList() ([]v1.APIGroup, error) {
	discoveryClient, err := getDiscoveryClient()
	if err != nil {
		return nil, err
	}
	apiList, err := discoveryClient.ServerGroups()
	if err != nil {
		return nil, err
	}
	return apiList.Groups, nil
}

func HasAPIResourceName(name string) bool {
	discoveryClient, err := getDiscoveryClient()
	if err != nil {
		return false
	}

	_, resourcesList, err := discoveryClient.ServerGroupsAndResources()
	if err != nil {
		return false
	}

	return HasAPIResourceNameInList(name, resourcesList)
}

func HasAPIResourceNameInList(name string, resources []*metav1.APIResourceList) bool {
	for _, l := range resources {
		for _, r := range l.APIResources {
			if r.Name == name {
				return true
			}
		}
	}

	return false
}

func GetValue(key string, defaultValue string) (value string) {
	value = key
	if len(key) < 1 {
		value = defaultValue
	}
	return value
}

func GetMapValue(value map[string]string, defaultValue map[string]string) map[string]string {
	ret := value
	if len(value) < 1 {
		ret = defaultValue
	}

	return ret
}

func MergeMaps(first map[string]string, second map[string]string) map[string]string {
	ret := make(map[string]string)
	for k, v := range first {
		ret[k] = v
	}

	for k, v := range second {
		ret[k] = v
	}

	return ret
}

func GetServerExposureStrategy(c *orgv1.CheCluster, defaultValue string) string {
	strategy := c.Spec.Server.ServerExposureStrategy
	if IsOpenShift {
		strategy = GetValue(strategy, defaultValue)
	} else {
		if strategy == "" {
			strategy = GetValue(c.Spec.K8s.IngressStrategy, defaultValue)
		}
	}

	return strategy
}

func IsTestMode() (isTesting bool) {
	testMode := os.Getenv("MOCK_API")
	if len(testMode) == 0 {
		return false
	}
	return true
}

func GetClusterPublicHostname(isOpenShift4 bool) (hostname string, err error) {
	// Could be set for debug scripts.
	CLUSTER_API_URL := os.Getenv("CLUSTER_API_URL")
	if CLUSTER_API_URL != "" {
		return CLUSTER_API_URL, nil
	}
	if isOpenShift4 {
		return getClusterPublicHostnameForOpenshiftV4()
	}
	return getClusterPublicHostnameForOpenshiftV3()
}

// getClusterPublicHostnameForOpenshiftV3 is a hacky way to get OpenShift API public DNS/IP
// to be used in OpenShift oAuth provider as baseURL
func getClusterPublicHostnameForOpenshiftV3() (hostname string, err error) {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client := &http.Client{}
	kubeApi := os.Getenv("KUBERNETES_PORT_443_TCP_ADDR")
	url := "https://" + kubeApi + "/.well-known/oauth-authorization-server"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	resp, err := client.Do(req)
	if err != nil {
		logrus.Errorf("An error occurred when getting API public hostname: %s", err)
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logrus.Errorf("An error occurred when getting API public hostname: %s", err)
		return "", err
	}
	var jsonData map[string]interface{}
	err = json.Unmarshal(body, &jsonData)
	if err != nil {
		logrus.Errorf("An error occurred when unmarshalling: %s", err)
		return "", err
	}
	hostname = jsonData["issuer"].(string)
	return hostname, nil
}

// getClusterPublicHostnameForOpenshiftV3 is a way to get OpenShift API public DNS/IP
// to be used in OpenShift oAuth provider as baseURL
func getClusterPublicHostnameForOpenshiftV4() (hostname string, err error) {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client := &http.Client{}
	kubeApi := os.Getenv("KUBERNETES_PORT_443_TCP_ADDR")
	url := "https://" + kubeApi + "/apis/config.openshift.io/v1/infrastructures/cluster"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	file, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		logrus.Errorf("Failed to locate token file: %s", err)
		return "", err
	}
	token := string(file)

	req.Header = http.Header{
		"Authorization": []string{"Bearer " + token},
	}
	resp, err := client.Do(req)
	if err != nil {
		logrus.Errorf("An error occurred when getting API public hostname: %s", err)
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		message := url + " - " + resp.Status
		logrus.Errorf("An error occurred when getting API public hostname: %s", message)
		return "", errors.New(message)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logrus.Errorf("An error occurred when getting API public hostname: %s", err)
		return "", err
	}
	var jsonData map[string]interface{}
	err = json.Unmarshal(body, &jsonData)
	if err != nil {
		logrus.Errorf("An error occurred when unmarshalling while getting API public hostname: %s", err)
		return "", err
	}
	switch status := jsonData["status"].(type) {
	case map[string]interface{}:
		hostname = status["apiServerURL"].(string)
	default:
		logrus.Errorf("An error occurred when unmarshalling while getting API public hostname: %s", body)
		return "", errors.New(string(body))
	}

	return hostname, nil
}

func GetDeploymentEnv(deployment *appsv1.Deployment, key string) (value string) {
	env := deployment.Spec.Template.Spec.Containers[0].Env
	for i := range env {
		name := env[i].Name
		if name == key {
			value = env[i].Value
			break
		}
	}
	return value
}

func GetDeploymentEnvVarSource(deployment *appsv1.Deployment, key string) (valueFrom *corev1.EnvVarSource) {
	env := deployment.Spec.Template.Spec.Containers[0].Env
	for i := range env {
		name := env[i].Name
		if name == key {
			valueFrom = env[i].ValueFrom
			break
		}
	}
	return valueFrom
}

func GetEnvByRegExp(regExp string) []corev1.EnvVar {
	var env []corev1.EnvVar
	for _, e := range os.Environ() {
		pair := strings.SplitN(e, "=", 2)
		envName := pair[0]
		rxp := regexp.MustCompile(regExp)
		if rxp.MatchString(envName) {
			envName = GetArchitectureDependentEnv(envName)
			env = append(env, corev1.EnvVar{Name: envName, Value: pair[1]})
		}
	}
	return env
}

// GetArchitectureDependentEnv returns environment variable dependending on architecture
// by adding "_<ARCHITECTURE>" suffix. If variable is not set then the default will be return.
func GetArchitectureDependentEnv(env string) string {
	archEnv := env + "_" + runtime.GOARCH
	if _, ok := os.LookupEnv(archEnv); ok {
		return archEnv
	}

	return env
}

// NewBoolPointer returns `bool` pointer to value in the memory.
// Unfortunately golang hasn't got syntax to create `bool` pointer.
func NewBoolPointer(value bool) *bool {
	variable := value
	return &variable
}

// IsOAuthEnabled returns true when oAuth is enable for CheCluster resource, otherwise false.
func IsOAuthEnabled(c *orgv1.CheCluster) bool {
	if c.Spec.Auth.OpenShiftoAuth != nil && *c.Spec.Auth.OpenShiftoAuth {
		return true
	}
	return false
}

// IsInitialOpenShiftOAuthUserEnabled returns true when initial Openshift oAuth user is enabled for CheCluster resource, otherwise false.
func IsInitialOpenShiftOAuthUserEnabled(c *orgv1.CheCluster) bool {
	if c.Spec.Auth.InitialOpenShiftOAuthUser != nil && *c.Spec.Auth.InitialOpenShiftOAuthUser {
		return true
	}
	return false
}

// IsWorkspaceInSameNamespaceWithChe return true when Che workspaces will be executed in the same namespace with Che, otherwise returns false.
func IsWorkspaceInSameNamespaceWithChe(cr *orgv1.CheCluster) bool {
	return GetWorkspaceNamespaceDefault(cr) == cr.Namespace
}

// GetWorkspaceNamespaceDefault - returns workspace namespace default strategy, which points on the namespaces used for workspaces execution.
func GetWorkspaceNamespaceDefault(cr *orgv1.CheCluster) string {
	if cr.Spec.Server.CustomCheProperties != nil {
		k8sNamespaceDefault := cr.Spec.Server.CustomCheProperties["CHE_INFRA_KUBERNETES_NAMESPACE_DEFAULT"]
		if k8sNamespaceDefault != "" {
			return k8sNamespaceDefault
		}
	}

	workspaceNamespaceDefault := cr.Namespace
	if IsOpenShift && IsOAuthEnabled(cr) {
		workspaceNamespaceDefault = "<username>-" + cr.Spec.Server.CheFlavor
	}
	return GetValue(cr.Spec.Server.WorkspaceNamespaceDefault, workspaceNamespaceDefault)
}

// IsDeleteOAuthInitialUser - returns true when initial Openshfit oAuth user must be deleted.
func IsDeleteOAuthInitialUser(cr *orgv1.CheCluster) bool {
	return cr.Spec.Auth.InitialOpenShiftOAuthUser != nil && !*cr.Spec.Auth.InitialOpenShiftOAuthUser && cr.Status.OpenShiftOAuthUserCredentialsSecret != ""
}

func GetResourceQuantity(value string, defaultValue string) resource.Quantity {
	if value != "" {
		return resource.MustParse(value)
	}
	return resource.MustParse(defaultValue)
}

// Finds Env by a given name
func FindEnv(envs []corev1.EnvVar, name string) *corev1.EnvVar {
	for _, env := range envs {
		if env.Name == name {
			return &env
		}
	}

	return nil
}

func ReadObject(yamlFile string, obj interface{}) error {
	data, err := ioutil.ReadFile(yamlFile)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(data, obj)
	if err != nil {
		return err
	}

	return nil
}

func ReloadCheCluster(client client.Client, cheCluster *orgv1.CheCluster) error {
	return client.Get(
		context.TODO(),
		types.NamespacedName{Name: cheCluster.Name, Namespace: cheCluster.Namespace},
		cheCluster)
}
