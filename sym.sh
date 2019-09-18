#!/bin/sh

. /opt/ibm/spectrumcomputing/profile.platform 
soamcontrol app disable symping7.2.1 -f
sleep 12
soamcontrol app enable symping7.2.1

symping $* -d
