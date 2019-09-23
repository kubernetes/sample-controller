#!/bin/bash

. /opt/ibm/spectrumcomputing/profile.platform 
. /opt/ibm/spectrumcomputing/soam/7.2.1/samples/Python/FaaS/profile.faas

egosh user logon -u Admin -x Admin
soamcontrol app disable FaaSPython -f
sleep 12
soamcontrol app enable FaaSPython

python /opt/ibm/spectrumcomputing/soam/7.2.1/samples/Python/FaaS/Client/SymClient.py $*
