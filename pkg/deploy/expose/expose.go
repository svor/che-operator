//
// Copyright (c) 2020-2020 Red Hat, Inc.
// This program and the accompanying materials are made
// available under the terms of the Eclipse Public License 2.0
// which is available at https://www.eclipse.org/legal/epl-2.0/
//
// SPDX-License-Identifier: EPL-2.0
//
// Contributors:
//   Red Hat, Inc. - initial API and implementation
//
package expose

import (
	orgv1 "github.com/eclipse-che/che-operator/pkg/apis/org/v1"
	"github.com/eclipse-che/che-operator/pkg/deploy"
	"github.com/eclipse-che/che-operator/pkg/deploy/gateway"
	"github.com/eclipse-che/che-operator/pkg/util"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/sirupsen/logrus"
	extentionsv1beta1 "k8s.io/api/extensions/v1beta1"
)

func Expose(
	deployContext *deploy.DeployContext,
	cheHost string,
	endpointName string,
	routeCustomSettings orgv1.RouteCustomSettings,
	ingressCustomSettings orgv1.IngressCustomSettings,
	component string) (endpont string, done bool, err error) {
	exposureStrategy := util.GetServerExposureStrategy(deployContext.CheCluster, deploy.DefaultServerExposureStrategy)
	var domain string
	var endpoint string
	var pathPrefix string
	var stripPrefix bool

	if endpointName == deploy.IdentityProviderName {
		pathPrefix = "auth"
		stripPrefix = false
	} else {
		pathPrefix = endpointName
		stripPrefix = true
	}
	if exposureStrategy == "multi-host" {
		// this won't get used on openshift, because there we're intentionally let Openshift decide on the domain name
		domain = endpointName + "-" + deployContext.CheCluster.Namespace + "." + deployContext.CheCluster.Spec.K8s.IngressDomain
		endpoint = domain
	} else {
		domain = cheHost
		if endpointName == deploy.IdentityProviderName {
			// legacy
			endpoint = domain
		} else {
			endpoint = domain + "/" + pathPrefix
		}
	}

	gatewayConfig := "che-gateway-route-" + endpointName
	singleHostExposureType := deploy.GetSingleHostExposureType(deployContext.CheCluster)
	useGateway := exposureStrategy == "single-host" && (util.IsOpenShift || singleHostExposureType == "gateway")

	if !util.IsOpenShift {
		if useGateway {
			cfg := gateway.GetGatewayRouteConfig(deployContext, gatewayConfig, "/"+pathPrefix, 10, "http://"+endpointName+":8080", stripPrefix)
			done, err := deploy.SyncConfigMapSpecToCluster(deployContext, &cfg)
			if !util.IsTestMode() {
				if !done {
					if err != nil {
						logrus.Error(err)
					}
					return "", false, err
				}
			}
			if _, err = deploy.DeleteNamespacedObject(deployContext, endpointName, &extentionsv1beta1.Ingress{}); err != nil {
				logrus.Error(err)
			}
		} else {
			done, err := deploy.SyncIngressToCluster(deployContext, endpointName, domain, endpointName, 8080, ingressCustomSettings, component)
			if !done {
				logrus.Infof("Waiting on ingress '%s' to be ready", endpointName)
				if err != nil {
					logrus.Error(err)
				}
				return "", false, err
			}
			if err := gateway.DeleteGatewayRouteConfig(gatewayConfig, deployContext); !util.IsTestMode() && err != nil {
				logrus.Error(err)
			}
		}
	} else {
		if useGateway {
			cfg := gateway.GetGatewayRouteConfig(deployContext, gatewayConfig, "/"+pathPrefix, 10, "http://"+endpointName+":8080", stripPrefix)
			done, err := deploy.SyncConfigMapSpecToCluster(deployContext, &cfg)
			if !done {
				if err != nil {
					logrus.Error(err)
				}
				return "", false, err
			}

			_, err = deploy.DeleteNamespacedObject(deployContext, endpointName, &routev1.Route{})
			if err != nil {
				logrus.Error(err)
			}
		} else {
			// the empty string for a host is intentional here - we let OpenShift decide on the hostname
			done, err := deploy.SyncRouteToCluster(deployContext, endpointName, "", endpointName, 8080, routeCustomSettings, component)
			if !done {
				if err != nil {
					logrus.Error(err)
				}
				return "", false, err
			}

			if err := gateway.DeleteGatewayRouteConfig(gatewayConfig, deployContext); !util.IsTestMode() && err != nil {
				logrus.Error(err)
			}

			route := &routev1.Route{}
			exists, err := deploy.GetNamespacedObject(deployContext, endpointName, route)
			if !exists {
				if err != nil {
					logrus.Error(err)
				}
				return "", false, err
			}

			endpoint = route.Spec.Host
		}
	}
	return endpoint, true, nil
}
