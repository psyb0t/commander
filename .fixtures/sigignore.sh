#!/bin/bash

# This script ignores SIGTERM but responds to SIGKILL
# Used for testing SIGKILL behavior

# Set up signal handler to ignore SIGTERM
trap '' TERM

# Run indefinitely until killed
while true; do
    sleep 1
done