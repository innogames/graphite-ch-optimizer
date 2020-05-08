#!/usr/bin/groovy
library 'adminsLib@master'

properties([
    parameters([
        string(defaultValue: '', description: 'deb-drop repository, see https://github.com/innogames/deb-drop/', name: 'REPO_NAME', trim: false),
    ])
])

// Remove builds in presented status, default is ['ABORTED', 'NOT_BUILT']
jobCommon.cleanNotFinishedBuilds()

node() {
ansiColor('xterm') {
    // Checkout repo and get info about current stage
    sh 'echo Initial env; env | sort'
    env.PACKAGE_NAME = 'graphite-ch-optimizer'
    try {
        stage('Checkout') {
            gitSteps checkout: true, changeBuildName: false
            sh 'set +x; echo "Environment variables after checkout:"; env|sort'
            env.NEW_VERSION = sh(returnStdout: true, script: 'make version').trim()
            currentBuild.displayName = "${currentBuild.number}: version ${env.NEW_VERSION}"
        }
        stage('Upload to deb-drop') {
            when(env.GIT_BRANCH_OR_TAG == 'tag' && jobCommon.launchedByUser() && env.REPO_NAME != '') {
                deb_package = "graphite-ch-optimizer_${env.NEW_VERSION}_amd64.deb"
                [deb_package, 'md5sum', 'sha256sum'].each { file->
                    sh "set -ex; wget https://github.com/innogames/graphite-ch-optimizer/releases/download/${env.GIT_BRANCH}/${file}"
                }
                ['md5sum', 'sha256sum'].each { sum->
                    sh "set -ex; ${sum} --ignore-missing --status -c ${sum}"
                }
                withCredentials([string(credentialsId: 'DEB_DROP_TOKEN', variable: 'DebDropToken')]) {
                    jobCommon.uploadPackage  file: deb_package, repo: env.REPO_NAME, token: DebDropToken
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
