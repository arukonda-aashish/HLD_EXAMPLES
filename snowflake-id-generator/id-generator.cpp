#include <iostream>
#include <chrono>
#include <cstdint>
#include <stdexcept>
#include <mutex>
#include <thread>

using namespace std;

class SnowFlake{
private:
  static const int SEQUENCE_BITS=12;
  static const int MACHINE_BITS=5;
  static const int DATACENTER_BITS=5;

  static const int64_t MAX_SEQUENCE=(1LL << SEQUENCE_BITS)-1;
  static const int64_t MAX_MACHINE_ID=(1LL<<MACHINE_BITS)-1;
  static const int64_t MAX_DATACENTER_ID=(1LL<< DATACENTER_BITS)-1;

  static const int MACHINE_SHIFT=SEQUENCE_BITS;
  static const int DATACENTER_SHIFT=SEQUENCE_BITS+MACHINE_BITS;
  static const int TIMESTAMP_SHIFT=SEQUENCE_BITS+MACHINE_BITS+DATACENTER_BITS;

  static const int64_t EPOCH = 1609459200000LL;

  int64_t machineId;
  int64_t datacenterId;
  int64_t lastTimestamp;
  int64_t  sequence;

  mutex mtx;
private:
   int64_t currentTimeMillis(){
      return chrono::duration_cast<chrono::milliseconds>(
            chrono::system_clock::now().time_since_epoch()
        ).count();
   }

   int64_t waitNextMillis(int64_t lastTs){
     int64_t ts=currentTimeMillis();
     while(ts<=lastTs){
        ts=currentTimeMillis();
     }
     return ts;
   }

public:
    SnowFlake(int64_t machineId,int64_t datacenterId)
        :machineId(machineId),datacenterId(datacenterId),
        lastTimestamp(-1),sequence(0){
           if(machineId >MAX_MACHINE_ID || machineId<0)
             throw invalid_argument("Invalide machine ID");
           if(datacenterId > MAX_DATACENTER_ID || datacenterId<0)
              throw invalid_argument("Invalid Datacenter ID");
        }
    int64_t nextId(){
        lock_guard<mutex> lock(mtx);

        int64_t timestamp=currentTimeMillis();
        cout << timestamp <<"\n";

        if(timestamp< lastTimestamp){
            throw runtime_error("Clock moved backwards");
        }
        if(timestamp==lastTimestamp){
            sequence=(sequence+1)& MAX_SEQUENCE;
            if(sequence==0){
                timestamp=waitNextMillis(lastTimestamp);
            }
        }else{
            sequence=0;
        }
        lastTimestamp=timestamp;

        int64_t id=((timestamp-EPOCH)<< TIMESTAMP_SHIFT) | 
                   (datacenterId << DATACENTER_SHIFT) | 
                   (machineId  << MACHINE_SHIFT) |
                     sequence;

        return id;
    }
    
};

int main(){
    SnowFlake generator(1,1);
    for(int i=0;i<10;i++){
        cout << generator.nextId() << endl;
    }
}