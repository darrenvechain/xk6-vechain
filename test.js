import { check, sleep } from "k6";
import http from "k6/http";
import vechain from "k6/x/vechain";

const stages = [
    { target: 44, duration: "10s" },
    { target: 44, duration: "10m" },
    { target: 0, duration: "1m" },
]

function repeatStages(cycle, times) {
    const out = [];
    for (let i = 0; i < times; i++) out.push(...cycle);
    return out;
}

export const options = {
    scenarios: {
        contacts: {
            executor: 'ramping-arrival-rate',
            // Start with `startRate` transactions per block. Eg set this to 10 to achieve 10 txs per block.
            startRate: 36,
            // Set the time unit to 10 seconds (ie. 1 block)
            timeUnit: '10s',
            // Pre-allocate necessary VUs.
            preAllocatedVUs: 50,
            maxVUs: 50,
            // Roughly 78 days running
            stages: repeatStages(stages, 10000),
            gracefulStop: '30s',
        },
    },
    tags: {
        test_name: "vechain-toolchain",
        test_run_id: 'v2.6.0'
    }
};

const url = "http://localhost:8669";

const thor = vechain.Client({
    url: url,
    mnemonic:
        "denial kitchen pet squirrel other broom bar gas better priority spoil cross",
    accounts: 100,
});

export default function (setup) {
    const rawTx = thor.newToolchainTransaction(setup.contracts[0]);
    const body = {
        raw: rawTx
    };

    console.log("sending tx");

    const res = http.post(`${url}/transactions`, JSON.stringify(body));

    check(res, {
        "is status 200": (r) => r.status === 200,
    });

    sleep(5);
}

export function setup() {
    console.log("Setting up test");
    const contracts = thor.deployToolchain(1);
    return {contracts};
}

export function teardown() {
    console.log("Tearing down test");
}
