FROM gitpod/workspace-full

RUN curl https://bazel.build/bazel-release.pub.gpg | sudo apt-key add - \
    echo "deb [arch=amd64] https://storage.googleapis.com/bazel-apt stable jdk1.8" | sudo tee /etc/apt/sources.list.d/bazel.list

RUN sudo apt-get update \
    && sudo apt-get install -y \
    bazel \
    && sudo rm -rf /var/lib/apt/lists/*