#!/bin/bash
#
# Copyright (c) 2012-2020 Red Hat, Inc.
# This program and the accompanying materials are made
# available under the terms of the Eclipse Public License 2.0
# which is available at https://www.eclipse.org/legal/epl-2.0/
#
# SPDX-License-Identifier: EPL-2.0
#

set -e
set -x

export OPERATOR_REPO=$(dirname $(dirname $(dirname $(dirname $(readlink -f "${BASH_SOURCE[0]}")))))
source "${OPERATOR_REPO}"/.github/bin/common.sh

# Stop execution on any error
trap "catchFinish" EXIT SIGINT

runTest() {
  "${OPERATOR_REPO}"/olm/testCatalogSource.sh "kubernetes" "nightly" ${NAMESPACE} "catalog" "my_image"
  startNewWorkspace
  waitWorkspaceStart
}

init
insecurePrivateDockerRegistry
runTest