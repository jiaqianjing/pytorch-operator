GIT_SHORT_COMMIT=$(git rev-parse --short HEAD)
echo "build start ..."
echo "docker build -f Dockerfile -t registry.cn-huhehaote.aliyuncs.com/lumo/pytorch-operator:${GIT_SHORT_COMMIT} ./"
docker build -f Dockerfile -t registry.cn-huhehaote.aliyuncs.com/lumo/pytorch-operator:${GIT_SHORT_COMMIT} ./

