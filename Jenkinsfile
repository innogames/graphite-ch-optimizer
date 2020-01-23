#!/usr/bin/groovy
library 'adminsLib@master'
import groovy.json.JsonOutput

properties([
    parameters([
        string(defaultValue: '', description: 'deb-drop repository, see https://github.com/innogames/deb-drop/', name: 'REPO_NAME', trim: false),
        booleanParam(defaultValue: false, description: 'Publish new packages and images or not. True for new releases, False by default.', name: 'PUBLISH'),
    ])
])

def build_packages(docker_image) {
    docker_image.inside("${jobCommon.dockerArgs()} -e GOPATH='${env.HOME}/go'") {
        sh '''\
            #!/bin/sh -ex
            make packages
            '''.stripIndent()
    }
}


// Remove builds in presented status, default is ['ABORTED', 'NOT_BUILT']
jobCommon.cleanNotFinishedBuilds()

node('docker') {
ansiColor('xterm') {
    // Checkout repo and get info about current stage
    sh 'echo Initial env; env | sort'
    env.PACKAGE_NAME = 'graphite-ch-optimizer'
    env.DOCKER_IMAGE = 'innogames/' + env.PACKAGE_NAME
    def img_builder
    def img_build
    try {
        stage('Checkout') {
            gitSteps checkout: true, changeBuildName: false
            sh 'set +x; echo "Environment variables after checkout:"; env|sort'
            env.NEW_VERSION = sh(returnStdout: true, script: 'make version').trim()
            currentBuild.displayName = "${currentBuild.number}: version ${env.NEW_VERSION}"
        }
        stage('Tests') {
            try {
                docker.image("${env.DOCKER_IMAGE}:builder").pull()
            } catch (all) {
                echo 'Unable to pull builder image, building from scratch'
            } finally {
                img_builder = docker.build(
                        "${env.DOCKER_IMAGE}:builder",
                        "--target builder --cache-from=${env.DOCKER_IMAGE}:builder ."
                        )
            }
            docker.withRegistry('', 'dockerIgSysadminsToken') {
                img_builder.push()
            }
            img_build = docker.build("${env.DOCKER_IMAGE}:build", '--target build .')
            img_build.inside("${jobCommon.dockerArgs()} -e GOPATH='${env.HOME}/go'") {
                sh '''\
                    #!/bin/sh -ex
                    make test
                    go get -u
                    if ! git diff --exit-code; then
                        echo The modules are changed, run 'go mod tidy' and commit changes
                        exit 1
                    fi
                    '''
            }
        }
        stage('Building') {
        when(jobCommon.launchedByUser()) {
            build_packages(img_build)

            if (env.REPO_NAME) {
                deb_packages = findFiles(glob: "*${env.NEW_VERSION}*deb")
                withCredentials([string(credentialsId: 'DEB_DROP_TOKEN', variable: 'DebDropToken')]) {
                    jobCommon.uploadPackage  file: deb_packages[0].path, repo: env.REPO_NAME, token: DebDropToken
                }
            }

        }
        when(env.GIT_BRANCH_OR_TAG == 'tag' && !jobCommon.launchedByUser()) {
            // TODO: implement github-api requests in library to publish new releases and assets
            echo 'Assets publishing will be here eventually'
            //build_packages(img_build)
            //withCredentials([usernamePassword(
            //    credentialsId: 'username/token',
            //    usernameVariable: 'USERNAME',
            //    passwordVariable: 'TOKEN'
            //)]) {
            //}
        }
        }
        cleanWs(notFailBuild: true)
    }
    catch (all) {
        currentBuild.result = 'FAILURE'
        error "Something wrong, exception is: ${all}"
        jobCommon.processException(all)
    }
    finally {
        jobCommon.postSlack()
    }
}
}
