#!/usr/bin/python3
# -*- python -*-

import argparse
import json
import logging
import os
import re
import socket
import ssl
import subprocess
import sys
from time import sleep
from urllib.error import HTTPError
from urllib.request import Request, urlopen

LOG_FORMAT = "%(asctime)s - %(filename)s - %(levelname)s: %(message)s"


class APIServer:
    """REST call to the k8s API server and automatic json (un)marshalling"""

    def __init__(self, namespace: str, token: str, cert: str, timeout: int = 10,
                 api_server: str = "https://kubernetes.default.svc"):
        self.api_server = api_server
        self.namespace = namespace
        self.token = token
        self.cert = cert
        self.timeout = timeout

    def get(self, url: str):
        context = ssl.create_default_context(cafile=self.cert)
        req = Request(self.api_server + url)
        req.add_header("Content-Type", "application/json")
        req.add_header("Authorization", "Bearer " + self.token)
        res = urlopen(req, timeout=self.timeout, context=context)
        if res.status == 200:
            content = res.read()
            data = json.loads(content)
            return data
        else:
            raise Exception("get error")

    def put(self, url: str, data: str):
        jsondata = json.dumps(data).encode()
        context = ssl.create_default_context(cafile=self.cert)
        req = Request(self.api_server + url, data=jsondata, method='PUT')
        req.add_header("Content-Type", "application/json")
        req.add_header("Authorization", "Bearer " + self.token)
        res = urlopen(req, timeout=self.timeout, context=context)
        return res


def retry(times, backoff=1.3):
    """Primitive retry decorator with exponential backoff"""

    def decorator(func):
        def wrapped_func(*args, **kwargs):
            attempt = 0
            sleep_time = 1
            last_error = None
            while attempt < times:
                try:
                    return func(*args, **kwargs)
                except HTTPError as e:
                    last_error = e
                    if e.code == 403:  # forbidden
                        raise Exception("API access no longer authorized")
                    elif e.code == 409:  # conflict
                        log.warning(f"object changed, retrying {func.__name__}()")
                except KeyError as e:
                    last_error = e
                    log.warning(f"expected input not present ({e}), retrying {func.__name__}()")
                # pass here if there was an caught exception
                sleep(sleep_time)
                sleep_time *= backoff
                attempt += 1
            raise Exception(f"Error after retrying {func.__name__} {times} times: {last_error}")
        return wrapped_func
    return decorator


@retry(10)
def get_cid(api: APIServer, namespace: str, pod_name: str) -> str:
    pod: dict = api.get(f"/api/v1/namespaces/{namespace}/pods/{pod_name}")
    if 'status' in pod and 'containerStatuses' in pod['status']:
        for cstatus in pod['status']['containerStatuses']:
            if cstatus['name'] == 'galera':
                # status of container should report 'running' for the containerID
                # key to be present in the returned status, otherwise a retry is attempted
                return cstatus['containerID']
    raise KeyError("ContainerID not found for 'galera'")


def inspect_galera_state():
    status = subprocess.run(['bash', '/var/lib/operator-scripts/detect_last_commit.sh'], capture_output=True)
    if status.returncode == 2:
        raise Exception("mysqld seems to be running, cannot inspect local state")
    assert status.returncode == 0
    detected = json.loads(status.stdout)
    return detected


@retry(10)
def push_state(api, namespace, galera_name, pod_name, detected):
    galera_cr = api.get(f"/apis/mariadb.openstack.org/v1beta1/namespaces/{namespace}/galeras/{galera_name}/status")
    attributes = galera_cr['status'].get('attributes', {})
    attributes[pod_name] = detected
    galera_cr['status']['attributes'] = attributes
    api.put(f"/apis/mariadb.openstack.org/v1beta1/namespaces/{namespace}/galeras/{galera_name}/status", galera_cr)


def report_state(push: bool):
    pod_name = socket.gethostname()
    galera_name = re.sub(r'-galera-[0-9]*', '', pod_name)

    svc_account = "/var/run/secrets/kubernetes.io/serviceaccount"
    with open(os.path.join(svc_account, "namespace")) as f:
        namespace = f.read()
    with open(os.path.join(svc_account, "token")) as f:
        token = f.read()
    cert = os.path.join(svc_account, "ca.crt")

    api = APIServer(namespace, token, cert)

    try:
        log.info("Retrieving the ID of the container we are running in")
        cid = get_cid(api, namespace, pod_name)
        log.info(f"found: {cid}")

        log.info(f"Extracting the Galera state from the local mariadb database on {pod_name}")
        detected = inspect_galera_state()
        log.info(f"found: {detected}")
        detected['containerID'] = cid

        if push:
            log.info(f"Saving the extracted Galera state into the status of Galera CR {galera_name} - {detected}")
            push_state(api, namespace, galera_name, pod_name, detected)
        else:
            log.info(f"Local galera state detected: {detected}")

    except Exception as e:
        log.error(f"Unexpected error while inspecting local galera state: {type(e).__name__}: {e}")
        sys.exit(1)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Report the state of the local mysql database")
    parser.add_argument("-p", "--push",
                        help="Push the computed state to the linked galera CR",
                        action=argparse.BooleanOptionalAction)
    parser.add_argument("-f", "--file",
                        help="Also log output of this script to file",
                        )
    opts = parser.parse_args()

    log = logging.getLogger(sys.argv[0])
    log.setLevel(logging.DEBUG)
    formatter = logging.Formatter(LOG_FORMAT)

    output = logging.StreamHandler(sys.stdout)
    output.setFormatter(formatter)
    log.addHandler(output)

    if opts.file:
        file_output = logging.FileHandler(opts.file, mode='a')
        file_output.setFormatter(formatter)
        log.addHandler(file_output)

    report_state(opts.push)
