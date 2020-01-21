#!/usr/bin/groovy
library 'adminsLib@master'
properties([
    parameters([
        string(defaultValue: '', description: 'deb-drop repository, see https://github.com/innogames/deb-drop/', name: 'REPO_NAME', trim: false),
        booleanParam(defaultValue: false, description: 'Publish new packages and images or not. True for new releases, False by default.', name: 'PUBLISH'),
    ])
])

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
            img_build = docker.build("${env.DOCKER_IMAGE}:build", '--target build .')
            img_build.inside("${jobCommon.dockerArgs()} -e GOPATH='${env.HOME}/go'") {
                sh 'make test'
            }
        }
        stage('Building') {
        when(jobCommon.launchedByUser()) {
            app_image = docker.build("${env.DOCKER_IMAGE}:latest")
            img_build.inside("${jobCommon.dockerArgs()} -e GOPATH='${env.HOME}/go'") {
                sh '''\
                    #!/bin/sh -ex
                    make packages
                    '''.stripIndent()
            }

            if (env.PUBLISH) {
                docker.withRegistry('', 'dockerIgSysadminsToken') {
                    img_builder.push()
                }
            }

            if (env.REPO_NAME) {
                deb_package = findFiles(glob: "*${env.NEW_VERSION}*deb")
                withCredentials([string(credentialsId: 'DEB_DROP_TOKEN', variable: 'DebDropToken')]) {
                    jobCommon.uploadPackage  file: deb_package, repo: env.REPO_NAME, token: DebDropToken
                }
            }
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
