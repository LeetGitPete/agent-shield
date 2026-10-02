# dev.ps1 - builds, deploys, stops and operates the stack on the local
# Kubernetes cluster (the one built into Docker Desktop).
#
#   .\dev.ps1           lists the commands
#   .\dev.ps1 deploy    builds the images and brings the stack up
#
# Works under Windows PowerShell 5.1.
#
# The script declares no parameters and reads $args instead. Windows
# PowerShell binds a dash-prefixed argument to a declared parameter whose
# name it is a prefix of, which would swallow flags meant for the attack
# simulation. With no parameters declared, everything after the command name
# reaches the tool as PowerShell parsed it. PowerShell itself splits an
# unquoted dash-prefixed argument at its first period, so give the tool's
# flags in the space form (-gap 1.5s), or quote the argument ('-gap=1.5s').
#
# Conventions: ASCII only (CI checks it). The exit code of every external
# command is checked and a failure stops the script. Error output is not
# redirected. No inline JSON is passed to an external command. Existence
# probes and removals use --ignore-not-found and test the output, so a
# missing object is not a failed command.

$ErrorActionPreference = 'Stop'

$Images = @('detector', 'triage', 'api', 'sensor')
$AppDeployments = @('detector', 'triage', 'api', 'sensor-acme', 'sensor-globex')
$InfraDeployments = @('rabbitmq', 'postgres', 'redis')
$AllDeployments = $AppDeployments + $InfraDeployments
$AppSelector = 'app in (' + ($AppDeployments -join ',') + ')'
$StackSelector = 'app in (' + ($AllDeployments -join ',') + ')'
$GeminiSecret = 'gemini'
$EnvFile = Join-Path $PSScriptRoot '.env'
$StackDownMessage = "The stack is down. 'dev.ps1 deploy' brings it up."

function Stop-Script([string]$Message) {
    Write-Host "dev.ps1: $Message"
    exit 1
}

# Called after every external command.
function Assert-LastExit([string]$What) {
    if ($LASTEXITCODE -ne 0) {
        Stop-Script "$What failed (exit code $LASTEXITCODE)."
    }
}

function Assert-NoArguments([string]$Command, [string[]]$Rest) {
    if ($Rest.Count -gt 0) {
        Stop-Script "'$Command' takes no arguments."
    }
}

function Show-Help {
    Write-Host 'dev.ps1 <command> - run the stack on the local Kubernetes cluster'
    Write-Host ''
    Write-Host '  help             list all commands'
    Write-Host '  build            build all images with the local tag'
    Write-Host '  deploy           build, apply the manifests, restart the applications and wait for them'
    Write-Host '  down             remove the stack from the cluster; deploy brings it back'
    Write-Host '  status           show the pods and the triage provider'
    Write-Host '  test             run the Go tests'
    Write-Host '  simulate-attack  publish a known attack to verify detection; flags: -agents N -gap 300ms -out-of-order -customer ID'
    Write-Host '  triage           triage mock|gemini: set the triage provider and wait for its rollout'
}

function Test-StackDeployed {
    $found = @(kubectl get deployments @AllDeployments --ignore-not-found -o name)
    Assert-LastExit 'Looking for the stack'
    return ($found.Count -gt 0)
}

function Assert-StackDeployed {
    if (-not (Test-StackDeployed)) {
        Stop-Script $StackDownMessage
    }
}

# Compose publishes the same host ports as the cluster's load-balancer services.
function Assert-ComposeDown {
    $running = @(docker compose ps -q)
    Assert-LastExit 'Checking docker compose'
    if ($running.Count -gt 0) {
        Stop-Script "The stack is running under docker compose, which uses the same host ports. Run 'docker compose down' first."
    }
}

function Invoke-Build {
    foreach ($image in $Images) {
        Write-Host "Building agent-shield-${image}:local"
        docker build --build-arg "SERVICE=$image" -t "agent-shield-${image}:local" .
        Assert-LastExit "Building the $image image"
    }
}

# Waits until every application Deployment has rolled out. Stops as soon as a
# pod of this rollout reports that its image cannot be pulled, because waiting
# cannot fix that. $EarlierPods are the pods that existed before the restart:
# one of them may still be failing to pull from an earlier, failed deploy.
function Wait-Applications([string[]]$EarlierPods) {
    $deadline = (Get-Date).AddSeconds(300)
    $waitingFor = ''
    while ($true) {
        Start-Sleep -Seconds 3

        $pods = @(kubectl get pods -l $AppSelector --ignore-not-found --no-headers -o 'custom-columns=NAME:.metadata.name,WAITING:.status.containerStatuses[*].state.waiting.reason')
        Assert-LastExit 'Listing the application pods'
        foreach ($line in $pods) {
            $name, $waiting = $line.Trim() -split '\s+'
            if ($EarlierPods -notcontains "pod/$name" -and $waiting -match 'ErrImagePull|ImagePullBackOff|ErrImageNeverPull') {
                Stop-Script "Pod $name cannot pull its image ($waiting): the cluster cannot get the locally built image. The cluster node pulls local images through Docker Desktop's registry mirror; 'kubectl describe pod $name' shows the pull error."
            }
        }

        $deployments = @(kubectl get deployments @AppDeployments --no-headers -o 'custom-columns=NAME:.metadata.name,GENERATION:.metadata.generation,OBSERVED:.status.observedGeneration,WANT:.spec.replicas,HAVE:.status.replicas,UPDATED:.status.updatedReplicas,AVAILABLE:.status.availableReplicas')
        Assert-LastExit 'Reading the rollout state'
        $pending = @()
        foreach ($line in $deployments) {
            # A count the cluster has not set yet prints as <none>.
            $name, $generation, $observed, $want, $have, $updated, $available = $line.Trim() -split '\s+' | ForEach-Object { if ($_ -eq '<none>') { '0' } else { $_ } }
            $rolledOut = ([int]$observed -ge [int]$generation) -and ($have -eq $want) -and ($updated -eq $want) -and ($available -eq $want)
            if (-not $rolledOut) {
                $pending += $name
            }
        }
        if ($pending.Count -eq 0) {
            return
        }
        if ((Get-Date) -gt $deadline) {
            Stop-Script "Not rolled out after 300 seconds: $($pending -join ', '). 'dev.ps1 status' shows the pods."
        }
        if (($pending -join ', ') -ne $waitingFor) {
            $waitingFor = $pending -join ', '
            Write-Host "Waiting for: $waitingFor"
        }
    }
}

# Returns the Gemini key from the local environment file, or an empty string
# when the file or the key is missing. The key is never printed.
function Get-GeminiKey {
    if (-not (Test-Path -LiteralPath $EnvFile)) {
        return ''
    }
    $key = ''
    foreach ($line in Get-Content -LiteralPath $EnvFile) {
        if ($line -match '^\s*(?:export\s+)?GEMINI_API_KEY\s*=(.*)$') {
            $value = $Matches[1].Trim()
            if ($value -match '^"([^"]*)"\s*(?:#.*)?$' -or $value -match "^'([^']*)'\s*(?:#.*)?$") {
                $value = $Matches[1] # a quoted value, with or without a comment after it
            } else {
                $value = ($value -split '\s+#', 2)[0] # an unquoted value ends at a comment
            }
            $key = $value # as in compose, the last assignment wins
        }
    }
    return $key
}

# Makes the Gemini secret match the local environment file: recreated when
# the file holds a key, removed when it holds none.
function Sync-GeminiSecret {
    $removed = @(kubectl delete secret $GeminiSecret --ignore-not-found)
    Assert-LastExit 'Removing the Gemini secret'
    $key = Get-GeminiKey
    if ($key) {
        $null = kubectl create secret generic $GeminiSecret "--from-literal=GEMINI_API_KEY=$key"
        Assert-LastExit 'Creating the Gemini secret'
        Write-Host "Gemini secret: created from the key in .env. Triage stays on mock until 'dev.ps1 triage gemini'."
    } elseif ($removed.Count -gt 0) {
        Write-Host "Gemini secret: removed, because .env holds no key. 'dev.ps1 triage gemini' needs one."
    } else {
        Write-Host "Gemini secret: none, because .env holds no key. 'dev.ps1 triage gemini' needs one."
    }
}

function Invoke-Deploy {
    Assert-ComposeDown
    Invoke-Build

    Write-Host 'Applying the infrastructure'
    kubectl apply -f k8s/infra.yaml
    Assert-LastExit 'Applying the infrastructure'
    foreach ($deployment in $InfraDeployments) {
        kubectl rollout status "deployment/$deployment" --timeout=300s
        Assert-LastExit "Waiting for $deployment"
    }

    # The manifest sets the triage provider to mock, so applying it puts a
    # stack that was switched to gemini back on mock.
    Write-Host 'Applying the applications'
    kubectl apply -f k8s/apps.yaml
    Assert-LastExit 'Applying the applications'

    # The image tag is fixed, so a rebuild does not start a rollout on its
    # own. A restarted pod pulls the current build.
    Write-Host 'Restarting the applications'
    $earlierPods = @(kubectl get pods -l $AppSelector --ignore-not-found -o name)
    Assert-LastExit 'Listing the application pods'
    kubectl rollout restart @($AppDeployments | ForEach-Object { "deployment/$_" })
    Assert-LastExit 'Restarting the applications'
    Wait-Applications $earlierPods

    # Last, after the rollout: no pod started by this deploy has seen a key.
    Sync-GeminiSecret

    Write-Host 'The stack is up. API: http://localhost:8080  broker management: http://localhost:15672'
}

function Invoke-Down {
    $removed = @()
    $removed += @(kubectl delete -f k8s/apps.yaml --ignore-not-found)
    Assert-LastExit 'Removing the applications'
    $removed += @(kubectl delete -f k8s/infra.yaml --ignore-not-found)
    Assert-LastExit 'Removing the infrastructure'
    $removed += @(kubectl delete secret $GeminiSecret --ignore-not-found)
    Assert-LastExit 'Removing the Gemini secret'

    $deadline = (Get-Date).AddSeconds(180)
    while ($true) {
        $pods = @(kubectl get pods -l $StackSelector --ignore-not-found -o name)
        Assert-LastExit 'Listing the pods'
        if ($pods.Count -eq 0) {
            break
        }
        if ((Get-Date) -gt $deadline) {
            Stop-Script "Pods are still present after 180 seconds: $($pods -join ', ')."
        }
        Start-Sleep -Seconds 2
    }

    if ($removed.Count -gt 0) {
        Write-Host 'Removed:'
        foreach ($line in $removed) {
            Write-Host "  $line"
        }
    } else {
        Write-Host 'Nothing was deployed.'
    }
    Write-Host 'The stack is down.'
}

function Show-Status {
    if (-not (Test-StackDeployed)) {
        Write-Host $StackDownMessage
        return
    }
    Write-Host 'Pods:'
    kubectl get pods -l $StackSelector --ignore-not-found
    Assert-LastExit 'Listing the pods'

    $triage = @(kubectl get deployment triage --ignore-not-found -o name)
    Assert-LastExit 'Looking for the triage Deployment'
    $provider = 'the triage Deployment is not deployed'
    if ($triage.Count -gt 0) {
        $provider = kubectl get deployment triage -o "jsonpath={.spec.template.spec.containers[0].env[?(@.name=='TRIAGE_PROVIDER')].value}"
        Assert-LastExit 'Reading the triage provider'
    }
    Write-Host ''
    Write-Host "Triage provider: $provider"
}

function Invoke-Test {
    go test ./...
    Assert-LastExit 'The Go tests'
}

function Invoke-SimulateAttack([string[]]$Rest) {
    Assert-StackDeployed
    # Run on the host against the broker's port on localhost.
    go run ./cmd/attacksim @Rest
    Assert-LastExit 'The attack simulation'
}

function Set-TriageProvider([string[]]$Rest) {
    if ($Rest.Count -ne 1 -or @('mock', 'gemini') -cnotcontains $Rest[0]) {
        Stop-Script 'Usage: dev.ps1 triage mock|gemini'
    }
    $provider = $Rest[0]
    Assert-StackDeployed

    if ($provider -eq 'gemini') {
        $secret = @(kubectl get secret $GeminiSecret --ignore-not-found -o name)
        Assert-LastExit 'Looking for the Gemini secret'
        if ($secret.Count -eq 0) {
            Stop-Script "The Gemini secret does not exist, so triage stays as it is. Put GEMINI_API_KEY in .env and run 'dev.ps1 deploy', which creates the secret."
        }
    }

    kubectl set env deployment/triage "TRIAGE_PROVIDER=$provider"
    Assert-LastExit 'Setting the triage provider'
    kubectl rollout status deployment/triage --timeout=300s
    Assert-LastExit 'Waiting for triage'
    Write-Host "Triage provider: $provider"
}

$command = 'help'
$rest = @()
if ($args.Count -gt 0) {
    $command = [string]$args[0]
}
if ($args.Count -gt 1) {
    $rest = @($args[1..($args.Count - 1)])
}

# The manifests, the build context and the Go module are addressed relative to the script.
Push-Location $PSScriptRoot
try {
    switch ($command) {
        'help' { Show-Help }
        'build' { Assert-NoArguments $command $rest; Invoke-Build }
        'deploy' { Assert-NoArguments $command $rest; Invoke-Deploy }
        'down' { Assert-NoArguments $command $rest; Invoke-Down }
        'status' { Assert-NoArguments $command $rest; Show-Status }
        'test' { Assert-NoArguments $command $rest; Invoke-Test }
        'simulate-attack' { Invoke-SimulateAttack $rest }
        'triage' { Set-TriageProvider $rest }
        default {
            Show-Help
            Write-Host ''
            Stop-Script "Unknown command '$command'."
        }
    }
} finally {
    Pop-Location
}
