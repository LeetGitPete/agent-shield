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
$AppDeployments = @('detector', 'triage', 'api', 'sensor')
$InfraDeployments = @('rabbitmq', 'postgres', 'redis')
$AllDeployments = $AppDeployments + $InfraDeployments
# One sensor Deployment per customer, from before the single fleet sensor.
$LegacySensors = @('sensor-acme', 'sensor-globex')
$AppSelector = 'app in (' + ($AppDeployments -join ',') + ')'
$StackSelector = 'app in (' + (($AllDeployments + $LegacySensors) -join ',') + ')'
$GeminiSecret = 'gemini'
$EnvFile = Join-Path $PSScriptRoot '.env'
$StackDownMessage = "The stack is down. 'dev.ps1 deploy' brings it up."

# KEDA scales the detector. It lives in its own namespace and outlives 'down'.
$KedaVersion = '2.21.0'
$KedaManifest = "https://github.com/kedacore/keda/releases/download/v$KedaVersion/keda-$KedaVersion.yaml"
$KedaNamespace = 'keda'
$KedaMetricsApi = 'v1beta1.external.metrics.k8s.io'
$ScaledObjectType = 'scaledobjects.keda.sh'
$ScaledObject = 'detector'

# The load levels: the sensor's average rate in events per second and the
# probability that an event is suspicious. Low is also what the sensor's
# manifest declares. High is 3.3 times the scaled object's rate target, so
# its average needs four detector replicas and its peaks reach five; it
# raises no findings.
$LoadLevels = @{
    low  = @{ Rate = '10'; Suspicious = '0.005' }
    high = @{ Rate = '460'; Suspicious = '0' }
}

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
    Write-Host '  deploy           build, install KEDA if missing, apply the manifests, restart the applications and wait for them'
    Write-Host '  down             remove the stack from the cluster; deploy brings it back; down all also removes KEDA'
    Write-Host '  status           show the pods, the autoscaler state, the load and the triage provider'
    Write-Host '  test             run the Go tests'
    Write-Host '  load             load low|high|off: set the average load of the simulated fleet, or switch it off'
    Write-Host '  clean            empty the queues, the shared state and the findings'
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

# Waits until no pod matches the selector.
function Wait-NoPods([string]$Selector, [int]$Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ($true) {
        $pods = @(kubectl get pods -l $Selector --ignore-not-found -o name)
        Assert-LastExit 'Listing the pods'
        if ($pods.Count -eq 0) {
            return
        }
        if ((Get-Date) -gt $deadline) {
            Stop-Script "Pods are still present after $Seconds seconds: $($pods -join ', ')."
        }
        Start-Sleep -Seconds 2
    }
}

# KEDA's resource type for scaled objects. On a cluster without KEDA the
# type is unknown, and a command that names it fails.
function Test-ScaledObjectType {
    $type = @(kubectl get crd $ScaledObjectType --ignore-not-found -o name)
    Assert-LastExit 'Looking for the scaled-object resource type'
    return ($type.Count -gt 0)
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

# Installs KEDA unless it is there already, then waits until it can serve a
# scaled object: its Deployments and the metrics API the autoscaler reads.
function Install-Keda {
    $installed = Test-ScaledObjectType
    if ($installed) {
        $operator = @(kubectl get deployment keda-operator -n $KedaNamespace --ignore-not-found -o name)
        Assert-LastExit 'Looking for KEDA'
        $installed = ($operator.Count -gt 0)
    }
    if ($installed) {
        Write-Host 'KEDA: already installed, so the install is skipped'
    } else {
        Write-Host "Installing KEDA $KedaVersion"
        # Server-side: one of its resource definitions is too large for the
        # annotation a client-side apply stores.
        $applied = @(kubectl apply --server-side -f $KedaManifest)
        Assert-LastExit 'Installing KEDA'
        Write-Host "KEDA: applied $($applied.Count) objects"
    }

    Write-Host 'Waiting for KEDA'
    kubectl wait --for=condition=Available deployment --all -n $KedaNamespace --timeout=300s
    Assert-LastExit "Waiting for KEDA's Deployments (its images come from ghcr.io, so the cluster needs registry access)"
    kubectl wait --for=condition=Available "apiservice/$KedaMetricsApi" --timeout=300s
    Assert-LastExit "Waiting for KEDA's metrics API"
}

function Invoke-Deploy {
    Assert-ComposeDown
    Invoke-Build
    Install-Keda

    Write-Host 'Applying the infrastructure'
    kubectl apply -f k8s/infra.yaml
    Assert-LastExit 'Applying the infrastructure'
    foreach ($deployment in $InfraDeployments) {
        kubectl rollout status "deployment/$deployment" --timeout=300s
        Assert-LastExit "Waiting for $deployment"
    }

    # The manifest sets the triage provider to mock and the sensor to the low
    # load level, so applying it puts a stack that was switched to gemini back
    # on mock and resets the load.
    Write-Host 'Applying the applications'
    kubectl apply -f k8s/apps.yaml
    Assert-LastExit 'Applying the applications'
    kubectl apply -f k8s/scaledobject.yaml
    Assert-LastExit 'Applying the scaled object'

    $legacy = @(kubectl delete deployment @LegacySensors --ignore-not-found)
    Assert-LastExit 'Removing the legacy sensor Deployments'
    if ($legacy.Count -gt 0) {
        foreach ($line in $legacy) {
            Write-Host "Removed a legacy sensor: $line"
        }
    } else {
        Write-Host 'No legacy sensor Deployments to remove.'
    }

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

# Removes the scaled object and returns kubectl's lines about it. KEDA holds
# the object until it has removed the autoscaler it created for it, so with
# KEDA not running the object stays. That is waited on for 30 seconds only;
# $script:LeftoverScaledObject then tells the caller it is still there.
function Remove-ScaledObject {
    $script:LeftoverScaledObject = $false
    if (-not (Test-ScaledObjectType)) {
        return @() # KEDA is not installed, so there is no scaled object
    }
    $removed = @(kubectl delete scaledobject $ScaledObject --ignore-not-found --wait=false)
    Assert-LastExit 'Removing the scaled object'

    $deadline = (Get-Date).AddSeconds(30)
    while ($true) {
        $left = @(kubectl get scaledobject $ScaledObject --ignore-not-found -o name)
        Assert-LastExit 'Looking for the scaled object'
        if ($left.Count -eq 0) {
            return $removed
        }
        if ((Get-Date) -gt $deadline) {
            Write-Host "The scaled object '$ScaledObject' is still present after 30 seconds: KEDA, which has to release it, is not running. Continuing with the rest."
            $script:LeftoverScaledObject = $true
            return @()
        }
        Start-Sleep -Seconds 2
    }
}

function Invoke-Down([string[]]$Rest) {
    if ($Rest.Count -gt 1 -or ($Rest.Count -eq 1 -and $Rest[0] -cne 'all')) {
        Stop-Script 'Usage: dev.ps1 down [all]'
    }
    $removeKeda = ($Rest.Count -eq 1)

    # The scaled object goes first, so the autoscaler does not recreate detector replicas.
    $removed = @()
    $removed += @(Remove-ScaledObject)
    $removed += @(kubectl delete -f k8s/apps.yaml --ignore-not-found)
    Assert-LastExit 'Removing the applications'
    $removed += @(kubectl delete -f k8s/infra.yaml --ignore-not-found)
    Assert-LastExit 'Removing the infrastructure'
    $removed += @(kubectl delete deployment @LegacySensors --ignore-not-found)
    Assert-LastExit 'Removing the legacy sensor Deployments'
    $removed += @(kubectl delete secret $GeminiSecret --ignore-not-found)
    Assert-LastExit 'Removing the Gemini secret'

    Wait-NoPods $StackSelector 180

    if ($removeKeda -and -not $script:LeftoverScaledObject) {
        # Removing KEDA's resource types removes every scaled object in the cluster.
        $keda = @(kubectl delete -f $KedaManifest --ignore-not-found)
        Assert-LastExit 'Removing KEDA'
        if ($keda.Count -gt 0) {
            $removed += "KEDA $KedaVersion ($($keda.Count) objects)"
        }
    }

    if ($removed.Count -gt 0) {
        Write-Host 'Removed:'
        foreach ($line in $removed) {
            Write-Host "  $line"
        }
    } else {
        Write-Host 'Nothing was deployed.'
    }
    if ($script:LeftoverScaledObject) {
        $kedaNote = ''
        if ($removeKeda) {
            $kedaNote = ' KEDA was left installed, because removing it would wait on that object.'
        }
        Stop-Script "The stack is down except for the scaled object scaledobject.keda.sh/$ScaledObject, which KEDA has not released.$kedaNote KEDA releases it once it is running again ('kubectl get pods -n $KedaNamespace' shows its pods); then run down again."
    }
    if ($removeKeda) {
        Write-Host 'The stack is down and KEDA is removed.'
    } else {
        Write-Host 'The stack is down.'
    }
}

# Reads one setting of the sensor Deployment's container.
function Get-SensorSetting([string]$Name) {
    $value = kubectl get deployment sensor -o "jsonpath={.spec.template.spec.containers[0].env[?(@.name=='$Name')].value}"
    Assert-LastExit "Reading $Name"
    return [string]$value
}

function Get-SensorReplicas {
    $replicas = kubectl get deployment sensor -o 'jsonpath={.spec.replicas}'
    Assert-LastExit 'Reading the sensor replica count'
    return [int]$replicas
}

# Describes the load as the sensor Deployment has it.
function Get-LoadDescription {
    $rate = Get-SensorSetting 'SENSOR_EVENTS_PER_SEC'
    $suspicious = Get-SensorSetting 'SENSOR_SUSPICIOUS_PROB'
    $customers = Get-SensorSetting 'SENSOR_CUSTOMERS'
    $agents = Get-SensorSetting 'SENSOR_AGENTS_PER_CUSTOMER'
    $replicas = Get-SensorReplicas

    $level = 'custom'
    foreach ($name in $LoadLevels.Keys) {
        if ($LoadLevels[$name].Rate -eq $rate -and $LoadLevels[$name].Suspicious -eq $suspicious) {
            $level = $name
        }
    }
    $settings = "$rate events per second on average, suspicious probability $suspicious, $customers customers with $agents agents each"
    if ($replicas -eq 0) {
        return "off (sensor replicas: 0; its settings are $settings)"
    }
    return "$level ($settings; sensor replicas: $replicas)"
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
    Write-Host 'Autoscaler:'
    if (-not (Test-ScaledObjectType)) {
        Write-Host '  KEDA is not installed.'
    } else {
        $object = @(kubectl get scaledobject $ScaledObject --ignore-not-found -o name)
        Assert-LastExit 'Looking for the scaled object'
        if ($object.Count -eq 0) {
            Write-Host "  The scaled object '$ScaledObject' is not applied."
        } else {
            kubectl get scaledobject $ScaledObject
            Assert-LastExit 'Reading the scaled object'
            # The autoscaler KEDA creates for the scaled object. Its targets
            # are the current and the target value of each trigger.
            kubectl get hpa "keda-hpa-$ScaledObject" --ignore-not-found
            Assert-LastExit 'Reading the autoscaler'
        }
    }

    $sensor = @(kubectl get deployment sensor --ignore-not-found -o name)
    Assert-LastExit 'Looking for the sensor Deployment'
    $load = 'the sensor Deployment is not deployed'
    if ($sensor.Count -gt 0) {
        $load = Get-LoadDescription
    }
    Write-Host ''
    Write-Host "Load: $load"
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

function Set-Load([string[]]$Rest) {
    if ($Rest.Count -ne 1 -or @('low', 'high', 'off') -cnotcontains $Rest[0]) {
        Stop-Script 'Usage: dev.ps1 load low|high|off'
    }
    $level = $Rest[0]
    Assert-StackDeployed

    if ($level -eq 'off') {
        kubectl scale deployment/sensor --replicas=0
        Assert-LastExit 'Scaling the sensor to zero'
    } else {
        $settings = $LoadLevels[$level]
        kubectl set env deployment/sensor "SENSOR_EVENTS_PER_SEC=$($settings.Rate)" "SENSOR_SUSPICIOUS_PROB=$($settings.Suspicious)"
        Assert-LastExit 'Setting the load'
        kubectl scale deployment/sensor --replicas=1 # back from off
        Assert-LastExit 'Scaling the sensor to one replica'
    }
    kubectl rollout status deployment/sensor --timeout=300s
    Assert-LastExit 'Waiting for the sensor'
    if ($level -eq 'off') {
        Wait-NoPods 'app=sensor' 120
    }
    Write-Host "Load: $(Get-LoadDescription)"
}

# Returns the ready and the unacknowledged message count of a queue, read
# with the broker's own tool in its pod. The management API would lag up to
# five seconds behind.
function Get-QueueCounts([string]$Queue) {
    $lines = @(kubectl exec deployment/rabbitmq -- rabbitmqctl -q --no-table-headers list_queues name messages_ready messages_unacknowledged)
    Assert-LastExit 'Reading the queues'
    foreach ($line in $lines) {
        $name, $ready, $unacknowledged = $line.Trim() -split '\s+'
        if ($name -eq $Queue) {
            return @([int]$ready, [int]$unacknowledged)
        }
    }
    Stop-Script "The broker has no queue named $Queue."
}

function Clear-Queue([string]$Queue) {
    kubectl exec deployment/rabbitmq -- rabbitmqctl -q purge_queue $Queue
    Assert-LastExit "Purging the $Queue queue"
}

# Waits until the consumers of a queue hold no message any more.
function Wait-QueueIdle([string]$Queue) {
    $deadline = (Get-Date).AddSeconds(120)
    while ($true) {
        $ready, $unacknowledged = Get-QueueCounts $Queue
        if ($unacknowledged -eq 0) {
            return
        }
        if ((Get-Date) -gt $deadline) {
            Stop-Script "The $Queue queue still has $unacknowledged unacknowledged messages after 120 seconds. 'dev.ps1 status' shows the pods."
        }
        Start-Sleep -Seconds 1
    }
}

# Empties the queues, the shared state and the findings. The sensor is
# stopped first, so nothing new arrives, and each queue is purged before its
# consumers are waited on, so they only finish what they already hold.
function Invoke-Clean {
    Assert-StackDeployed
    $sensorReplicas = Get-SensorReplicas

    Write-Host 'Stopping the sensor'
    kubectl scale deployment/sensor --replicas=0
    Assert-LastExit 'Scaling the sensor to zero'
    Wait-NoPods 'app=sensor' 120

    Write-Host 'Emptying the queues'
    Clear-Queue 'events'
    Clear-Queue 'events.dlq'
    Wait-QueueIdle 'events' # the detectors finish their events, which can still request triage
    Clear-Queue 'triage'
    Wait-QueueIdle 'triage' # triage finishes its findings before they are removed

    Write-Host 'Emptying the shared state'
    kubectl exec deployment/redis -- redis-cli flushall
    Assert-LastExit 'Flushing Redis'

    # A plain truncate: the id sequence goes on, so no id is used twice.
    Write-Host 'Emptying the findings'
    kubectl exec deployment/postgres -- psql -U postgres -d agentshield -c 'TRUNCATE findings'
    Assert-LastExit 'Truncating the findings'

    # Back to the count it had: still zero after 'load off'.
    kubectl scale deployment/sensor "--replicas=$sensorReplicas"
    Assert-LastExit 'Restoring the sensor'
    kubectl rollout status deployment/sensor --timeout=300s
    Assert-LastExit 'Waiting for the sensor'
    Write-Host "Clean: the queues, the shared state and the findings are empty. Load: $(Get-LoadDescription)"
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
        'down' { Invoke-Down $rest }
        'status' { Assert-NoArguments $command $rest; Show-Status }
        'test' { Assert-NoArguments $command $rest; Invoke-Test }
        'simulate-attack' { Invoke-SimulateAttack $rest }
        'triage' { Set-TriageProvider $rest }
        'load' { Set-Load $rest }
        'clean' { Assert-NoArguments $command $rest; Invoke-Clean }
        default {
            Show-Help
            Write-Host ''
            Stop-Script "Unknown command '$command'."
        }
    }
} finally {
    Pop-Location
}
