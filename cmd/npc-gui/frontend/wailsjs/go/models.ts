export namespace main {
	
	export class NPCConfig {
	    id: string;
	    name: string;
	    serverAddr: string;
	    vkey: string;
	    connType: string;
	    proxyUrl: string;
	    logLevel: string;
	    autoReconnect: boolean;
	    skipVerify: boolean;
	    disableP2P: boolean;
	    protoVersion: number;
	    dnsServer: string;
	    keepAlive: number;
	    configFilePath: string;
	    isActive: boolean;
	
	    static createFrom(source: any = {}) {
	        return new NPCConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.serverAddr = source["serverAddr"];
	        this.vkey = source["vkey"];
	        this.connType = source["connType"];
	        this.proxyUrl = source["proxyUrl"];
	        this.logLevel = source["logLevel"];
	        this.autoReconnect = source["autoReconnect"];
	        this.skipVerify = source["skipVerify"];
	        this.disableP2P = source["disableP2P"];
	        this.protoVersion = source["protoVersion"];
	        this.dnsServer = source["dnsServer"];
	        this.keepAlive = source["keepAlive"];
	        this.configFilePath = source["configFilePath"];
	        this.isActive = source["isActive"];
	    }
	}

}

