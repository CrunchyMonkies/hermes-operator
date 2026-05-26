# hermes operator

golang

This will be a kubernetes operator for the hermes agent

https://github.com/nousresearch/hermes-agent

will need comprehensive CRDs to outline the configuration and deployment of hermes agents, this should also cover the activation of the default skills

will need a executor/reloader for the hermes agent to apply configuration and changes from the k8s documents

include brew in the images for startup and runtime application installation, this should map and install in a writable pvc and should not need sudo, 

the crds should allow the installation of packages via apt or brew

~/.local/bin should be available for the execution path and ~/.local should be mapped to a pvc

a skill for this should be added and activated automatically on the hermes agent

create dockerfiles for each component and push to harbor.bne1.ouchi.com.au/applications/ on build

all pvc mounts should use the same pvc and we should be able to set the size